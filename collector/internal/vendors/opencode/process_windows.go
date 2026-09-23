//go:build windows

package opencode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/centauri-ai/coslash/collector/internal/winprocess"
	"golang.org/x/sys/windows"
)

var listTUIProcessTimeout = 5 * time.Second

var (
	currentUserExecutable = winprocess.CurrentUserExecutable
	runTUIProcessQuery    = func(ctx context.Context) ([]byte, error) {
		return exec.CommandContext(
			ctx,
			"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", listOpenCodeProcesses,
		).Output()
	}
)

const listOpenCodeProcesses = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
$processes = @(Get-CimInstance Win32_Process -Filter "Name = 'opencode.exe'" | ForEach-Object {
    [pscustomobject]@{
        PID = [int]$_.ProcessId
        StartedAt = [long](($_.CreationDate.ToUniversalTime() - [datetime]'1970-01-01').TotalMilliseconds)
        Executable = [string]$_.ExecutablePath
        CommandLine = [string]$_.CommandLine
    }
})
ConvertTo-Json -Compress -InputObject $processes`

type windowsTUIProcess struct {
	PID         int    `json:"PID"`
	StartedAt   int64  `json:"StartedAt"`
	Executable  string `json:"Executable"`
	CommandLine string `json:"CommandLine"`
}

func listTUIProcesses() ([]tuiProcess, error) {
	return listTUIProcessesContext(context.Background())
}

func listTUIProcessesContext(ctx context.Context) ([]tuiProcess, error) {
	ctx, cancel := context.WithTimeout(ctx, listTUIProcessTimeout)
	defer cancel()
	output, err := runTUIProcessQuery(ctx)
	if err != nil {
		return nil, err
	}
	return parseWindowsTUIProcesses(output)
}

func parseWindowsTUIProcesses(output []byte) ([]tuiProcess, error) {
	var records []windowsTUIProcess
	if err := json.Unmarshal(output, &records); err != nil {
		return nil, err
	}
	processes := make([]tuiProcess, 0, len(records))
	for _, record := range records {
		if record.PID <= 0 || record.StartedAt <= 0 {
			continue
		}
		executable, err := currentUserExecutable(uint32(record.PID))
		if err != nil || !strings.EqualFold(filepath.Clean(executable), filepath.Clean(record.Executable)) ||
			!strings.EqualFold(filepath.Base(executable), "opencode.exe") {
			continue
		}
		args, err := windows.DecomposeCommandLine(record.CommandLine)
		if err != nil || len(args) == 0 {
			continue
		}
		project, sessionID, fork, tui := parseTUIArgs(args[1:])
		if !tui {
			continue
		}
		processes = append(processes, tuiProcess{
			pid: record.PID, startedAt: record.StartedAt, project: project,
			sessionID: sessionID, fork: fork,
		})
	}
	return processes, nil
}

func processWorkingDirectory(pid int) string {
	return processWorkingDirectoryContext(context.Background(), pid)
}

func processWorkingDirectoryContext(ctx context.Context, pid int) string {
	if ctx.Err() != nil {
		return ""
	}
	if pid <= 0 {
		return ""
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid),
	)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	var basic windows.PROCESS_BASIC_INFORMATION
	size := uint32(unsafe.Sizeof(basic))
	if err := windows.NtQueryInformationProcess(
		handle, windows.ProcessBasicInformation, unsafe.Pointer(&basic), size, &size,
	); err != nil || basic.PebBaseAddress == nil {
		return ""
	}
	var peb windows.PEB
	if readProcessMemory(handle, uintptr(unsafe.Pointer(basic.PebBaseAddress)), &peb) != nil ||
		peb.ProcessParameters == nil {
		return ""
	}
	var parameters windows.RTL_USER_PROCESS_PARAMETERS
	if readProcessMemory(handle, uintptr(unsafe.Pointer(peb.ProcessParameters)), &parameters) != nil {
		return ""
	}
	path := parameters.CurrentDirectory.DosPath
	if path.Buffer == nil || path.Length == 0 || path.Length%2 != 0 {
		return ""
	}
	buffer := make([]uint16, int(path.Length)/2)
	if windows.ReadProcessMemory(
		handle, uintptr(unsafe.Pointer(path.Buffer)), (*byte)(unsafe.Pointer(&buffer[0])),
		uintptr(path.Length), nil,
	) != nil {
		return ""
	}
	return windows.UTF16ToString(buffer)
}

func readProcessMemory[T any](handle windows.Handle, address uintptr, value *T) error {
	return windows.ReadProcessMemory(
		handle, address, (*byte)(unsafe.Pointer(value)), unsafe.Sizeof(*value), nil,
	)
}

func replaceFile(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
