//go:build windows

package cursor

import (
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/winfolders"
)

// GlobalStorage is the Cursor IDE globalStorage directory for home.
func GlobalStorage(home string) string {
	return filepath.Join(winfolders.RoamingAppData(home), "Cursor", "User", "globalStorage")
}
