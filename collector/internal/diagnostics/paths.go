package diagnostics

import (
	"path/filepath"
	"strings"
)

func displayPath(home, path string) string {
	if home == "" || path == "" {
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
		return message
	}
	return strings.ReplaceAll(message, filepath.Clean(home), "~")
}
