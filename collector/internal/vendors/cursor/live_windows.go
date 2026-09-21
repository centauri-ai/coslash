//go:build windows

package cursor

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cursorErrorMoreData      = 234
	cursorRMSessionKeyLength = 32
	cursorRMMaxAppName       = 255
	cursorRMMaxServiceName   = 63
)

var (
	cursorIDEProcessRunning   = cursorProcessRunning
	cursorCLIStoreProcesses   = processesUsingCursorStore
	cursorProcessExecutable   = processExecutable
	cursorRestartManager      = windows.NewLazySystemDLL("rstrtmgr.dll")
	cursorRMStartSession      = cursorRestartManager.NewProc("RmStartSession")
	cursorRMRegisterResources = cursorRestartManager.NewProc("RmRegisterResources")
	cursorRMGetList           = cursorRestartManager.NewProc("RmGetList")
	cursorRMEndSession        = cursorRestartManager.NewProc("RmEndSession")
)

type cursorRMUniqueProcess struct {
	PID       uint32
	StartTime windows.Filetime
}

type cursorRMProcessInfo struct {
	Process          cursorRMUniqueProcess
	AppName          [cursorRMMaxAppName + 1]uint16
	ServiceShortName [cursorRMMaxServiceName + 1]uint16
	ApplicationType  uint32
	AppStatus        uint32
	TSSessionID      uint32
	Restartable      int32
}

func loadLiveSessions() map[string]string {
	live := map[string]string{}
	home, err := os.UserHomeDir()
	if err != nil {
		return live
	}
	for _, store := range cursorChatStores(home, nil) {
		pids, err := cursorCLIStoreProcesses(store)
		if err != nil || !containsCursorAgentProcess(home, pids) {
			continue
		}
		id := canonicalCursorID(filepath.Base(filepath.Dir(store)))
		if transcriptIDPattern.MatchString(id) {
			live[id] = entrypointCLI
		}
	}
	if cursorIDEProcessRunning() {
		id := selectedCursorIDEChat(home)
		if transcriptIDPattern.MatchString(id) {
			if lane, exists := live[id]; exists && lane != entrypointIDE {
				live[id] = ""
			} else {
				live[id] = entrypointIDE
			}
		}
	}
	return live
}

func containsCursorAgentProcess(home string, pids []uint32) bool {
	for _, pid := range pids {
		path, err := cursorProcessExecutable(pid)
		if err == nil && isCursorAgentExecutable(home, path) {
			return true
		}
	}
	return false
}

func isCursorAgentExecutable(home, path string) bool {
	root := filepath.Join(home, "AppData", "Local", "cursor-agent")
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	if strings.EqualFold(relative, "node.exe") {
		return true
	}
	parts := strings.Split(relative, string(filepath.Separator))
	return len(parts) == 3 && strings.EqualFold(parts[0], "versions") && strings.EqualFold(parts[2], "node.exe")
}

func processExecutable(pid uint32) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func processesUsingCursorStore(path string) (pids []uint32, err error) {
	var session uint32
	var key [cursorRMSessionKeyLength + 1]uint16
	if code, _, _ := cursorRMStartSession.Call(
		uintptr(unsafe.Pointer(&session)),
		0,
		uintptr(unsafe.Pointer(&key[0])),
	); code != 0 {
		return nil, fmt.Errorf("RmStartSession: %w", syscall.Errno(code))
	}
	defer func() {
		if code, _, _ := cursorRMEndSession.Call(uintptr(session)); code != 0 && err == nil {
			err = fmt.Errorf("RmEndSession: %w", syscall.Errno(code))
		}
	}()

	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	paths := []*uint16{pathPointer}
	if code, _, _ := cursorRMRegisterResources.Call(
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
	code, _, _ := cursorRMGetList.Call(
		uintptr(session),
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&count)),
		0,
		uintptr(unsafe.Pointer(&rebootReasons)),
	)
	if code == 0 {
		return nil, nil
	}
	if code != cursorErrorMoreData {
		return nil, fmt.Errorf("RmGetList: %w", syscall.Errno(code))
	}
	for {
		processes := make([]cursorRMProcessInfo, needed)
		count = uint32(len(processes))
		code, _, _ = cursorRMGetList.Call(
			uintptr(session),
			uintptr(unsafe.Pointer(&needed)),
			uintptr(unsafe.Pointer(&count)),
			uintptr(unsafe.Pointer(&processes[0])),
			uintptr(unsafe.Pointer(&rebootReasons)),
		)
		if code == cursorErrorMoreData {
			continue
		}
		if code != 0 {
			return nil, fmt.Errorf("RmGetList: %w", syscall.Errno(code))
		}
		pids = make([]uint32, count)
		for i := range count {
			pids[i] = processes[i].Process.PID
		}
		return pids, nil
	}
}

func selectedCursorIDEChat(home string) string {
	path := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	db, err := openCursorDB(path)
	if err != nil {
		return ""
	}
	defer db.Close()
	var id string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursor/glass.selectedAgent'`).Scan(&id); err != nil && err != sql.ErrNoRows {
		return ""
	}
	return canonicalCursorID(id)
}

func cursorProcessRunning() bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return false
	}
	for {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "Cursor.exe") {
			return true
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			return false
		}
	}
}
