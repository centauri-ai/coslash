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

var batteryPercent = regexp.MustCompile(`([0-9]{1,3})%`)

// LocalConditions reads available OS hints. The explicit metered override also
// lets a user pause sync on a connection whose OS does not expose a cost hint.
func LocalConditions(ctx context.Context) (bool, int, error) {
	if os.Getenv("COSLASH_SYNC_METERED") == "1" {
		return true, -1, nil
	}
	if os.Getenv("COSLASH_SYNC_OFFLINE") == "1" {
		return false, -1, ErrPaused
	}
	if value := os.Getenv("COSLASH_SYNC_BATTERY_PERCENT"); value != "" {
		percent, err := strconv.Atoi(value)
		if err != nil || percent < 0 || percent > 100 {
			return false, -1, ErrPaused
		}
		return false, percent, nil
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		body, err := exec.CommandContext(probe, "pmset", "-g", "batt").Output()
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
		body, err := exec.CommandContext(probe, "nmcli", "-t", "-f", "GENERAL.METERED", "device", "show").Output()
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
