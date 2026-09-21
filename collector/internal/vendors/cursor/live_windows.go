//go:build windows

package cursor

import (
	"context"
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
	cursorProcessCommandLine  = processCommandLine
	cursorCLIResumeSessionIDs = loadCursorCLIResumeSessionIDs
	cursorSameFile            = sameFile
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
	return loadLiveSessionsContext(context.Background())
}

func loadLiveSessionsContext(ctx context.Context) map[string]string {
	live := map[string]string{}
	if ctx.Err() != nil {
		return live
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return live
	}
	for id := range cursorCLIResumeSessionIDs(home) {
		if ctx.Err() != nil {
			return live
		}
		live[id] = entrypointCLI
	}
	for _, store := range cursorChatStores(home, nil) {
		if ctx.Err() != nil {
			return live
		}
		if !containsCursorAgentProcess(home, cursorCLIStorePIDs(store)) {
			continue
		}
		id := canonicalCursorID(filepath.Base(filepath.Dir(store)))
		if transcriptIDPattern.MatchString(id) {
			live[id] = entrypointCLI
		}
	}
	if ctx.Err() == nil && cursorIDEProcessRunning() {
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

func loadCursorCLIResumeSessionIDs(home string) map[string]bool {
	ids := map[string]bool{}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ids
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if windows.Process32First(snapshot, &entry) != nil {
		return ids
	}
	for {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "node.exe") {
			path, pathErr := cursorProcessExecutable(entry.ProcessID)
			commandLine, commandErr := cursorProcessCommandLine(entry.ProcessID)
			if pathErr == nil && commandErr == nil && isCursorAgentExecutable(home, path) {
				if id := cursorResumeID(home, path, commandLine); id != "" {
					ids[id] = true
				}
			}
		}
		if windows.Process32Next(snapshot, &entry) != nil {
			return ids
		}
	}
}

func cursorResumeID(home, executable, commandLine string) string {
	arguments, err := windows.DecomposeCommandLine(commandLine)
	if err != nil || len(arguments) < 2 {
		return ""
	}
	relative, ok := cursorAgentExecutableRelative(home, executable)
	if !ok {
		return ""
	}
	expectedEntrypoints := []string{
		filepath.Join(filepath.Dir(executable), "index.js"),
		filepath.Join(home, "AppData", "Local", "cursor-agent", filepath.Dir(relative), "index.js"),
	}
	if !equalAnyPath(arguments[1], expectedEntrypoints) {
		return ""
	}
	for index := 2; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			return ""
		}
		if argument == "--resume" && index+1 < len(arguments) {
			id := canonicalCursorID(arguments[index+1])
			if transcriptIDPattern.MatchString(id) {
				return id
			}
		}
		if value, ok := strings.CutPrefix(argument, "--resume="); ok {
			id := canonicalCursorID(value)
			if transcriptIDPattern.MatchString(id) {
				return id
			}
		}
	}
	return ""
}

func cursorCLIStorePIDs(store string) []uint32 {
	seen := map[uint32]bool{}
	var pids []uint32
	for _, path := range []string{store, store + "-wal", store + "-shm"} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		matches, err := cursorCLIStoreProcesses(path)
		if err != nil {
			continue
		}
		for _, pid := range matches {
			if !seen[pid] {
				seen[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	return pids
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
	_, ok := cursorAgentExecutableRelative(home, path)
	return ok
}

func cursorAgentExecutableRelative(home, path string) (string, bool) {
	root := filepath.Join(home, "AppData", "Local", "cursor-agent")
	relative, err := filepath.Rel(root, path)
	if err == nil && validRelativePath(relative) && isCursorAgentRelativeExecutable(relative) {
		return relative, true
	}
	packagesRoot := filepath.Join(home, "AppData", "Local", "Packages")
	virtualRelative, err := filepath.Rel(packagesRoot, path)
	if err != nil || !validRelativePath(virtualRelative) {
		return "", false
	}
	parts := strings.Split(virtualRelative, string(filepath.Separator))
	if len(parts) < 5 || !strings.EqualFold(parts[1], "LocalCache") || !strings.EqualFold(parts[2], "Local") || !strings.EqualFold(parts[3], "cursor-agent") {
		return "", false
	}
	relative = filepath.Join(parts[4:]...)
	logicalPath := filepath.Join(root, relative)
	return relative, isCursorAgentRelativeExecutable(relative) && cursorSameFile(path, logicalPath)
}

func sameFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func validRelativePath(relative string) bool {
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func isCursorAgentRelativeExecutable(relative string) bool {
	if strings.EqualFold(relative, "node.exe") {
		return true
	}
	parts := strings.Split(relative, string(filepath.Separator))
	return len(parts) == 3 && strings.EqualFold(parts[0], "versions") && strings.EqualFold(parts[2], "node.exe")
}

func equalAnyPath(path string, candidates []string) bool {
	path = filepath.Clean(path)
	for _, candidate := range candidates {
		if strings.EqualFold(path, filepath.Clean(candidate)) {
			return true
		}
	}
	return false
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

func processCommandLine(pid uint32) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)
	var size uint32
	err = windows.NtQueryInformationProcess(process, windows.ProcessCommandLineInformation, nil, 0, &size)
	if err != windows.STATUS_INFO_LENGTH_MISMATCH || size < uint32(unsafe.Sizeof(windows.NTUnicodeString{})) {
		return "", err
	}
	buffer := make([]byte, size)
	if err := windows.NtQueryInformationProcess(
		process, windows.ProcessCommandLineInformation, unsafe.Pointer(&buffer[0]), size, &size,
	); err != nil {
		return "", err
	}
	value := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
	if value.Buffer == nil || value.Length == 0 || value.Length%2 != 0 {
		return "", nil
	}
	bufferStart := uintptr(unsafe.Pointer(&buffer[0]))
	bufferEnd := bufferStart + uintptr(len(buffer))
	valueStart := uintptr(unsafe.Pointer(value.Buffer))
	valueEnd := valueStart + uintptr(value.Length)
	if valueStart < bufferStart || valueEnd < valueStart || valueEnd > bufferEnd {
		return "", fmt.Errorf("process command line points outside result buffer")
	}
	return windows.UTF16ToString(unsafe.Slice(value.Buffer, int(value.Length)/2)), nil
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
