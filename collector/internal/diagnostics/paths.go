package diagnostics

import (
	"path/filepath"
	"strings"
)

func displayPath(home, path string) string {
	if path == "" {
		return path
	}
	if home == "" {
		if filepath.IsAbs(path) {
			return "<redacted>"
		}
		return path
	}
	cleanHome := filepath.Clean(home)
	cleanPath := filepath.Clean(path)
	if cleanPath == cleanHome {
		return "~"
	}
	prefix := cleanHome + string(filepath.Separator)
	if strings.HasPrefix(cleanPath, prefix) {
		return filepath.Join("~", strings.TrimPrefix(cleanPath, prefix))
	}
	return path
}

func displayError(home, message string) string {
	if home == "" {
		return "details redacted because the home directory is unavailable"
	}
	cleanHome := filepath.Clean(home)
	redacted := strings.ReplaceAll(message, cleanHome, "~")
	homeSlug := strings.ReplaceAll(cleanHome, string(filepath.Separator), "-")
	return strings.ReplaceAll(redacted, homeSlug, "~")
}
