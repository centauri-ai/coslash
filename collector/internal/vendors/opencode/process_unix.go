//go:build !windows

package opencode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func listTUIProcesses() ([]tuiProcess, error) {
	return listTUIProcessesContext(context.Background())
}

func listTUIProcessesContext(ctx context.Context) ([]tuiProcess, error) {
	output, err := exec.CommandContext(ctx, "ps", "-ww", "-axo", "pid=,lstart=,command=").Output()
	if err != nil {
		return nil, err
	}
	return parseTUIProcesses(string(output)), nil
}

func parseTUIProcesses(output string) []tuiProcess {
	var processes []tuiProcess
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || filepath.Base(fields[6]) != "opencode" {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		started, err := time.ParseInLocation(
			"Mon Jan 2 15:04:05 2006",
			strings.Join(fields[1:6], " "),
			time.Local,
		)
		if err != nil {
			continue
		}
		project, sessionID, fork, tui := parseTUIArgs(fields[7:])
		if !tui {
			continue
		}
		processes = append(processes, tuiProcess{
			pid: pid, startedAt: started.UnixMilli(), project: project,
			sessionID: sessionID, fork: fork,
		})
	}
	return processes
}

func processWorkingDirectory(pid int) string {
	return processWorkingDirectoryContext(context.Background(), pid)
}

func processWorkingDirectoryContext(ctx context.Context, pid int) string {
	output, err := exec.CommandContext(
		ctx,
		"lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn",
	).Output()
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n")
		}
	}
	return ""
}

func replaceFile(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
