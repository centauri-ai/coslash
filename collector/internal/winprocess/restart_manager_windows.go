//go:build windows

package winprocess

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	errorMoreData    = 234
	sessionKeyLength = 32
	maxAppName       = 255
	maxServiceName   = 63
)

var (
	restartManager    = windows.NewLazySystemDLL("rstrtmgr.dll")
	startSession      = restartManager.NewProc("RmStartSession")
	registerResources = restartManager.NewProc("RmRegisterResources")
	getList           = restartManager.NewProc("RmGetList")
	endSession        = restartManager.NewProc("RmEndSession")
)

type uniqueProcess struct {
	PID       uint32
	StartTime windows.Filetime
}

type processInfo struct {
	Process          uniqueProcess
	AppName          [maxAppName + 1]uint16
	ServiceShortName [maxServiceName + 1]uint16
	ApplicationType  uint32
	AppStatus        uint32
	TSSessionID      uint32
	Restartable      int32
}

func ProcessesUsingFiles(paths []string) (pids []uint32, err error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var session uint32
	var key [sessionKeyLength + 1]uint16
	if code, _, _ := startSession.Call(uintptr(unsafe.Pointer(&session)), 0, uintptr(unsafe.Pointer(&key[0]))); code != 0 {
		return nil, fmt.Errorf("RmStartSession: %w", syscall.Errno(code))
	}
	defer func() {
		if code, _, _ := endSession.Call(uintptr(session)); code != 0 && err == nil {
			err = fmt.Errorf("RmEndSession: %w", syscall.Errno(code))
		}
	}()

	pointers := make([]*uint16, len(paths))
	for index, path := range paths {
		pointer, pointerErr := windows.UTF16PtrFromString(path)
		if pointerErr != nil {
			return nil, pointerErr
		}
		pointers[index] = pointer
	}
	if code, _, _ := registerResources.Call(
		uintptr(session), uintptr(len(pointers)), uintptr(unsafe.Pointer(&pointers[0])), 0, 0, 0, 0,
	); code != 0 {
		return nil, fmt.Errorf("RmRegisterResources: %w", syscall.Errno(code))
	}
	runtime.KeepAlive(pointers)

	var needed, count, rebootReasons uint32
	code, _, _ := getList.Call(
		uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&rebootReasons)),
	)
	if code == 0 {
		return nil, nil
	}
	if code != errorMoreData {
		return nil, fmt.Errorf("RmGetList: %w", syscall.Errno(code))
	}
	for {
		processes := make([]processInfo, needed)
		count = uint32(len(processes))
		code, _, _ = getList.Call(
			uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&processes[0])), uintptr(unsafe.Pointer(&rebootReasons)),
		)
		if code == errorMoreData {
			continue
		}
		if code != 0 {
			return nil, fmt.Errorf("RmGetList: %w", syscall.Errno(code))
		}
		pids = make([]uint32, count)
		for index := range count {
			pids[index] = processes[index].Process.PID
		}
		return pids, nil
	}
}
