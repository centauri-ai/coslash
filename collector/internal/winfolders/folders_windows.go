//go:build windows

package winfolders

import (
	"os"
	"path/filepath"
	"strings"
)

func RoamingAppData(home string) string {
	if !isCurrentHome(home) {
		return filepath.Join(home, "AppData", "Roaming")
	}
	if path, err := os.UserConfigDir(); err == nil && path != "" {
		return path
	}
	return filepath.Join(home, "AppData", "Roaming")
}

func LocalAppData(home string) string {
	if !isCurrentHome(home) {
		return filepath.Join(home, "AppData", "Local")
	}
	if path, err := os.UserCacheDir(); err == nil && path != "" {
		return path
	}
	return filepath.Join(home, "AppData", "Local")
}

func ProgramFiles() []string {
	seen := map[string]bool{}
	var paths []string
	for _, name := range []string{"ProgramW6432", "ProgramFiles"} {
		path := strings.TrimSpace(os.Getenv(name))
		key := strings.ToLower(filepath.Clean(path))
		if path != "" && !seen[key] {
			seen[key] = true
			paths = append(paths, path)
		}
	}
	return paths
}

func isCurrentHome(home string) bool {
	current, err := os.UserHomeDir()
	return err == nil && strings.EqualFold(filepath.Clean(home), filepath.Clean(current))
}
