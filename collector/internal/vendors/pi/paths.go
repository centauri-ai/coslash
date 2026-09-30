package pi

import (
	"os"
	"path/filepath"
	"strings"
)

func Root() (string, error) {
	dir := os.Getenv("PI_CODING_AGENT_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".pi", "agent")
	}
	resolved, err := ResolveDirectory(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, "sessions"), nil
}

// ResolveDirectory uses the same tilde and cwd semantics as Pi session discovery.
func ResolveDirectory(dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/"))
	}
	return filepath.Abs(dir)
}
