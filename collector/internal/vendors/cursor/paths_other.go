//go:build !windows

package cursor

import "path/filepath"

func cursorGlobalStorage(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage")
}
