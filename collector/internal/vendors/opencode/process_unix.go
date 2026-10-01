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
	return scanOpenCodeProcesses(ctx, false)
}

func listOpenCodeProcessesContext(ctx context.Context) ([]tuiProcess, error) {
	return scanOpenCodeProcesses(ctx, true)
}

func scanOpenCodeProcesses(ctx context.Context, all bool) ([]tuiProcess, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := boundedCommandOutput(exec.CommandContext(ctx, "ps", "-ww", "-axo", "pid=,lstart=,ucomm=,command="), 4<<20)
	if err != nil {
		return nil, err
	}
	return parseOpenCodeProcesses(string(output), all), nil
}

func parseTUIProcesses(output string) []tuiProcess {
	return parseOpenCodeProcesses(output, false)
}

func parseOpenCodeProcesses(output string, all bool) []tuiProcess {
	if len(output) > 4<<20 {
		if !all {
			return nil
		}
		return []tuiProcess{{}}
	}
	var processes []tuiProcess
	lines := 0
	for line := range strings.SplitSeq(output, "\n") {
		lines++
		if lines > 65536 || len(line) > 64<<10 {
			if !all {
				return nil
			}
			return []tuiProcess{{}}
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			for _, field := range fields {
				if all && strings.EqualFold(filepath.Base(strings.Trim(field, "\"'")), "opencode") {
					processes = append(processes, tuiProcess{})
					break
				}
			}
			continue
		}
		// ucomm identifies the executable independently of filenames in argv.
		if fields[6] != "opencode" {
			if all && strings.Contains(fields[6], "/") {
				processes = append(processes, tuiProcess{})
			}
			continue
		}
		executable := -1
		for i := 7; i < len(fields); i++ {
			field := strings.Trim(fields[i], "\"'")
			if i > 7 && (filepath.IsAbs(field) || strings.HasPrefix(field, "-")) {
				break
			}
			if strings.EqualFold(filepath.Base(field), "opencode") && (i == 7 || strings.Contains(field, "/")) {
				executable = i
				break
			}
		}
		if executable < 0 {
			if all {
				processes = append(processes, tuiProcess{})
			}
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			if all {
				processes = append(processes, tuiProcess{})
			}
			continue
		}
		started, err := time.ParseInLocation(
			"Mon Jan 2 15:04:05 2006",
			strings.Join(fields[1:6], " "),
			time.Local,
		)
		if err != nil {
			if all {
				processes = append(processes, tuiProcess{})
			}
			continue
		}
		project, sessionID, fork, tui := parseTUIArgs(fields[executable+1:])
		if !tui && !all {
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := boundedCommandOutput(exec.CommandContext(
		ctx,
		"lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn",
	), 4<<20)
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
