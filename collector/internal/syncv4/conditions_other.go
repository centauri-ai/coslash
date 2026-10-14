//go:build !windows

package syncv4

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const conditionsTimeout = 2 * time.Second

var batteryPercent = regexp.MustCompile(`([0-9]{1,3})%`)

func platformConditions(ctx context.Context) (bool, int, error) {
	switch runtime.GOOS {
	case "darwin":
		body, err := exec.CommandContext(ctx, "pmset", "-g", "batt").Output()
		if err != nil {
			return false, -1, err
		}
		if !strings.Contains(string(body), "Battery Power") {
			return false, -1, nil
		}
		match := batteryPercent.FindSubmatch(body)
		if len(match) != 2 {
			return false, -1, ErrPaused
		}
		percent, _ := strconv.Atoi(string(match[1]))
		return false, percent, nil
	case "linux":
		body, err := exec.CommandContext(ctx, "nmcli", "-t", "-f", "GENERAL.METERED", "device", "show").Output()
		if err == nil && strings.Contains(strings.ToLower(string(body)), "yes") {
			return true, -1, nil
		}
		batteries, _ := filepath.Glob("/sys/class/power_supply/BAT*/capacity")
		for _, capacity := range batteries {
			status, err := os.ReadFile(filepath.Join(filepath.Dir(capacity), "status"))
			if err != nil || !strings.EqualFold(strings.TrimSpace(string(status)), "discharging") {
				continue
			}
			value, err := os.ReadFile(capacity)
			if err != nil {
				return false, -1, err
			}
			percent, err := strconv.Atoi(strings.TrimSpace(string(value)))
			if err != nil {
				return false, -1, err
			}
			return false, percent, nil
		}
	}
	return false, -1, nil
}
