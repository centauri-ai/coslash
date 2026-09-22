//go:build windows

package cursor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unsafe"

	"github.com/centauri-ai/coslash/collector/internal/winfolders"
	"github.com/centauri-ai/coslash/collector/internal/winprocess"
	"golang.org/x/sys/windows"
)

const maxWindowsLiveCursorStores = 256

var (
	cursorIDEProcessRunning   = cursorProcessRunning
	cursorCLIStoreProcesses   = winprocess.ProcessesUsingFiles
	cursorProcessExecutable   = winprocess.CurrentUserExecutable
	cursorProcessCommandLine  = processCommandLine
	cursorCLIResumeSessionIDs = loadCursorCLIResumeSessionIDs
	cursorSameFile            = sameFile
)

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
	for store := range liveCursorStoresContext(ctx, home, cursorChatStores(home, nil)) {
		if ctx.Err() != nil {
			return live
		}
		id := canonicalCursorID(filepath.Base(filepath.Dir(store)))
		if transcriptIDPattern.MatchString(id) {
			live[id] = entrypointCLI
		}
	}
	if ctx.Err() == nil && cursorIDEProcessRunning(home) {
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
		filepath.Join(winfolders.LocalAppData(home), "cursor-agent", filepath.Dir(relative), "index.js"),
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

func liveCursorStores(home string, stores []string) map[string]bool {
	return liveCursorStoresContext(context.Background(), home, stores)
}

func liveCursorStoresContext(ctx context.Context, home string, stores []string) map[string]bool {
	stores = newestCursorStores(stores, maxWindowsLiveCursorStores)
	storeByPath := map[string]string{}
	var paths []string
	for _, store := range stores {
		for _, path := range []string{store, store + "-wal", store + "-shm"} {
			if _, err := os.Stat(path); err == nil {
				storeByPath[path] = store
				paths = append(paths, path)
			}
		}
	}
	accepted := map[uint32]bool{}
	checked := map[uint32]bool{}
	used := map[string]bool{}
	var inspect func([]string)
	inspect = func(group []string) {
		if len(group) == 0 || ctx.Err() != nil {
			return
		}
		pids, err := cursorCLIStoreProcesses(group)
		if err != nil {
			return
		}
		hasCursorAgent := false
		for _, pid := range pids {
			if !checked[pid] {
				checked[pid] = true
				path, pathErr := cursorProcessExecutable(pid)
				accepted[pid] = pathErr == nil && isCursorAgentExecutable(home, path)
			}
			hasCursorAgent = hasCursorAgent || accepted[pid]
		}
		if !hasCursorAgent {
			return
		}
		if len(group) == 1 {
			used[storeByPath[group[0]]] = true
			return
		}
		middle := len(group) / 2
		inspect(group[:middle])
		inspect(group[middle:])
	}
	inspect(paths)
	return used
}

func newestCursorStores(stores []string, limit int) []string {
	if len(stores) <= limit {
		return slices.Clone(stores)
	}
	type candidate struct {
		path       string
		modifiedAt int64
	}
	candidates := make([]candidate, len(stores))
	for index, path := range stores {
		candidates[index].path = path
		for _, activePath := range []string{path, path + "-wal", path + "-shm"} {
			if info, err := os.Stat(activePath); err == nil {
				candidates[index].modifiedAt = max(candidates[index].modifiedAt, info.ModTime().UnixNano())
			}
		}
	}
	slices.SortFunc(candidates, func(left, right candidate) int {
		if left.modifiedAt > right.modifiedAt {
			return -1
		}
		if left.modifiedAt < right.modifiedAt {
			return 1
		}
		return strings.Compare(left.path, right.path)
	})
	result := make([]string, limit)
	for index := range result {
		result[index] = candidates[index].path
	}
	return result
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
	local := winfolders.LocalAppData(home)
	root := filepath.Join(local, "cursor-agent")
	relative, err := filepath.Rel(root, path)
	if err == nil && validRelativePath(relative) && isCursorAgentRelativeExecutable(relative) {
		return relative, true
	}
	packagesRoot := filepath.Join(local, "Packages")
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
	if err := windows.NtQueryInformationProcess(process, windows.ProcessCommandLineInformation, unsafe.Pointer(&buffer[0]), size, &size); err != nil {
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

func cursorProcessRunning(home string) bool {
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
			path, pathErr := cursorProcessExecutable(entry.ProcessID)
			if pathErr == nil && isCursorIDEExecutable(home, path) {
				return true
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			return false
		}
	}
}

func isCursorIDEExecutable(home, path string) bool {
	candidates := []string{filepath.Join(winfolders.LocalAppData(home), "Programs", "cursor", "Cursor.exe")}
	for _, root := range winfolders.ProgramFiles() {
		candidates = append(candidates, filepath.Join(root, "cursor", "Cursor.exe"))
	}
	return equalAnyPath(path, candidates)
}
