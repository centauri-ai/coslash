//go:build windows

package codex

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	errorMoreData      = 234
	rmSessionKeyLength = 32
	rmMaxAppName       = 255
	rmMaxServiceName   = 63
)

var (
	restartManager      = windows.NewLazySystemDLL("rstrtmgr.dll")
	rmStartSession      = restartManager.NewProc("RmStartSession")
	rmRegisterResources = restartManager.NewProc("RmRegisterResources")
	rmGetList           = restartManager.NewProc("RmGetList")
	rmEndSession        = restartManager.NewProc("RmEndSession")
)

type rmUniqueProcess struct {
	PID       uint32
	StartTime windows.Filetime
}

type rmProcessInfo struct {
	Process          rmUniqueProcess
	AppName          [rmMaxAppName + 1]uint16
	ServiceShortName [rmMaxServiceName + 1]uint16
	ApplicationType  uint32
	AppStatus        uint32
	TSSessionID      uint32
	Restartable      int32
}

func loadLiveSessions() (map[string]struct{}, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	return loadLiveSessionsFromFiles(files)
}

func loadLiveSessionsFromFiles(files []string) (map[string]struct{}, error) {
	live := map[string]struct{}{}
	for _, file := range files {
		pids, err := processesUsingRollout(file)
		if err != nil {
			return nil, fmt.Errorf("rollout %q: %w", file, err)
		}
		if id := sessionIDForOpenRollout(file, pids); id != "" {
			live[id] = struct{}{}
		}
	}
	return live, nil
}

func processesUsingRollout(path string) ([]uint32, error) {
	var session uint32
	var key [rmSessionKeyLength + 1]uint16
	if code, _, _ := rmStartSession.Call(
		uintptr(unsafe.Pointer(&session)),
		0,
		uintptr(unsafe.Pointer(&key[0])),
	); code != 0 {
		return nil, fmt.Errorf("RmStartSession: %w", syscall.Errno(code))
	}
	defer rmEndSession.Call(uintptr(session))

	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	paths := []*uint16{pathPointer}
	if code, _, _ := rmRegisterResources.Call(
		uintptr(session),
		1,
		uintptr(unsafe.Pointer(&paths[0])),
		0,
		0,
		0,
		0,
	); code != 0 {
		return nil, fmt.Errorf("RmRegisterResources: %w", syscall.Errno(code))
	}
	runtime.KeepAlive(paths)

	var needed, count, rebootReasons uint32
	code, _, _ := rmGetList.Call(
		uintptr(session),
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&count)),
		0,
		uintptr(unsafe.Pointer(&rebootReasons)),
	)
	if code == 0 {
		return nil, nil
	}
	if code != errorMoreData {
		return nil, fmt.Errorf("RmGetList: %w", syscall.Errno(code))
	}

	for {
		processes := make([]rmProcessInfo, needed)
		count = uint32(len(processes))
		code, _, _ = rmGetList.Call(
			uintptr(session),
			uintptr(unsafe.Pointer(&needed)),
			uintptr(unsafe.Pointer(&count)),
			uintptr(unsafe.Pointer(&processes[0])),
			uintptr(unsafe.Pointer(&rebootReasons)),
		)
		if code == errorMoreData {
			continue
		}
		if code != 0 {
			return nil, fmt.Errorf("RmGetList: %w", syscall.Errno(code))
		}
		pids := make([]uint32, count)
		for i := range count {
			pids[i] = processes[i].Process.PID
		}
		return pids, nil
	}
}
