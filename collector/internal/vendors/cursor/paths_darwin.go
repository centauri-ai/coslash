package cursor

import "path/filepath"

const cursorIDEProcess = "Cursor"

// GlobalStorage is the Cursor IDE globalStorage directory for home.
func GlobalStorage(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage")
}
