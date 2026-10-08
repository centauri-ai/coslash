package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var checkInVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func normalizedVersion() string {
	value := strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if value == "dev" || value == "0.0.0-dev" {
		if os.Getenv("COSLASH_ALLOW_DEV_VERSION") == "1" {
			return "0.0.0-dev"
		}
		return "0.0.0"
	}
	if len(value) <= 128 && checkInVersionPattern.MatchString(value) {
		return value
	}
	if os.Getenv("COSLASH_ALLOW_DEV_VERSION") == "1" {
		return "0.0.0-dev"
	}
	return "0.0.0"
}

func detectedInstallChannel() string {
	executable, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	resolved := executable
	if realPath, err := filepath.EvalSymlinks(executable); err == nil {
		resolved = realPath
	}
	home, _ := os.UserHomeDir()
	return installChannelForPaths(executable, resolved, home, os.Getenv("LOCALAPPDATA"), runtime.GOOS)
}

func installChannelForPaths(executable, resolved, home, localAppData, goos string) string {
	paths := []string{normalizeInstallPath(executable, goos), normalizeInstallPath(resolved, goos)}
	if goos == "windows" {
		root := normalizeInstallPath(path.Join(normalizeInstallPath(localAppData, goos), "Programs", "coSlash"), goos)
		for _, candidate := range paths {
			if pathWithin(candidate, root, true) {
				return "windows-script"
			}
		}
		return "unknown"
	}
	for _, candidate := range paths {
		if containsPathSegment(candidate, "Cellar") {
			return "brew"
		}
	}
	localBin := normalizeInstallPath(path.Join(normalizeInstallPath(home, goos), ".local", "bin"), goos)
	for _, candidate := range paths {
		if pathWithin(candidate, "/usr/local/bin", false) || (localBin != "" && pathWithin(candidate, localBin, false)) {
			return "script"
		}
	}
	return "unknown"
}

func normalizeInstallPath(value, goos string) string {
	value = path.Clean(strings.ReplaceAll(value, `\`, "/"))
	if goos == "windows" {
		value = strings.ToLower(strings.ReplaceAll(value, `\`, "/"))
	}
	return strings.TrimRight(value, "/")
}

func pathWithin(candidate, root string, caseInsensitive bool) bool {
	if caseInsensitive {
		candidate, root = strings.ToLower(candidate), strings.ToLower(root)
	}
	return candidate == root || strings.HasPrefix(candidate, root+"/")
}

func containsPathSegment(value, segment string) bool {
	for _, part := range strings.Split(value, "/") {
		if part == segment {
			return true
		}
	}
	return false
}
