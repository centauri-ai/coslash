//go:build !windows

package pi

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcessStartIdentity matches the extension's OS-derived identity, not its clock.
func ProcessStartIdentity(pid int) (string, error) {
	return processStartIdentityContext(context.Background(), pid)
}
func processStartIdentityContext(ctx context.Context, pid int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); err == nil {
		end := strings.LastIndexByte(string(data), ')')
		if end >= 0 {
			fields := strings.Fields(string(data)[end+1:])
			if len(fields) > 19 {
				boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
				if err != nil {
					return "", err
				}
				return "linux:" + strings.TrimSpace(string(boot)) + ":" + fields[19], nil
			}
		}
	}
	command := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	command.Env = append(os.Environ(), "LC_ALL=C")
	data, err := command.Output()
	if err != nil {
		return "", err
	}
	value := strings.Join(strings.Fields(string(data)), " ")
	if value == "" {
		return "", errors.New("missing process start identity")
	}
	return "ps:" + value, nil
}

func processAbsentContext(ctx context.Context, pid int) bool {
	// A permission-related signal failure is not proof of exit.
	if _, err := processStartIdentityContext(ctx, pid); err == nil {
		return false
	}
	err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "pid=").Run()
	exit, ok := err.(*exec.ExitError)
	return ok && exit.ExitCode() == 1
}
