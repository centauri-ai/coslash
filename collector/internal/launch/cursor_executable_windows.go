//go:build windows

package launch

import (
	"os"
	"os/exec"
	"path/filepath"
)

func CursorExecutable(home string) string {
	path := filepath.Join(home, "AppData", "Local", "Programs", "cursor", "Cursor.exe")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path
	}
	path, _ = exec.LookPath("cursor")
	return path
}

func CursorCLIExecutable(home string) string {
	if path, err := exec.LookPath("agent"); err == nil {
		return path
	}
	path := filepath.Join(home, "AppData", "Local", "cursor-agent", "agent.cmd")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path
	}
	return ""
}
