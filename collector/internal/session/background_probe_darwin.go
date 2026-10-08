package session

import (
	"os"
	"path/filepath"
	"strings"
)

// Background enrichment must not cause macOS to request access to a project
// folder merely because an old transcript mentions it.
func backgroundFilesystemProbeAllowed(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	path = filepath.Clean(path)
	if withinPath(path, "/Volumes") || withinPath(path, "/Network") {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, relative := range []string{
		"Desktop", "Documents", "Downloads",
		filepath.Join("Library", "Mobile Documents"),
		filepath.Join("Library", "CloudStorage"),
	} {
		if withinPath(path, filepath.Join(home, relative)) {
			return false
		}
	}
	return true
}

func withinPath(path, root string) bool {
	return strings.EqualFold(path, root) ||
		len(path) > len(root) && path[len(root)] == filepath.Separator && strings.EqualFold(path[:len(root)], root)
}
