//go:build !windows && !darwin

package cursor

import (
	"os"
	"path/filepath"
)

// The Linux build of Cursor is an Electron app named "cursor" that keeps its
// user data under the XDG config directory.
const cursorIDEProcess = "cursor"

// GlobalStorage is the Cursor IDE globalStorage directory for home.
func GlobalStorage(home string) string {
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "Cursor", "User", "globalStorage")
}
