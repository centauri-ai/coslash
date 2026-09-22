//go:build windows

package launch

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/winfolders"
)

func CursorExecutable(home string) string {
	candidates := []string{filepath.Join(winfolders.LocalAppData(home), "Programs", "cursor", "Cursor.exe")}
	for _, root := range winfolders.ProgramFiles() {
		candidates = append(candidates, filepath.Join(root, "cursor", "Cursor.exe"))
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	path, _ := exec.LookPath("cursor")
	return path
}

func CursorCLIExecutable(home string) string {
	path := filepath.Join(winfolders.LocalAppData(home), "cursor-agent", "agent.ps1")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path
	}
	if path, err := exec.LookPath("agent"); err == nil {
		return path
	}
	return ""
}
