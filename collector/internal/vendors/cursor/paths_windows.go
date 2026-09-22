//go:build windows

package cursor

import (
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/winfolders"
)

func cursorGlobalStorage(home string) string {
	return filepath.Join(winfolders.RoamingAppData(home), "Cursor", "User", "globalStorage")
}
