//go:build !windows

package launch

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func CursorExecutable(home string) string {
	for _, path := range []string{
		filepath.Join(home, "Applications", "Cursor.app", "Contents", "MacOS", "Cursor"),
		"/Applications/Cursor.app/Contents/MacOS/Cursor",
	} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return path
		}
	}
	path, _ := exec.LookPath("cursor")
	return path
}

func CursorCLIExecutable(string) string {
	path, _ := exec.LookPath(settings.CursorExecutable())
	return path
}
