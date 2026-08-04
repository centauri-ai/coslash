//go:build !darwin

package claude

import "os"

// birthtime has no portable source outside darwin, so callers fall back to the
// modification time. Fork ordering only needs birthtime to break ties, and the
// collector ships for macOS.
func birthtime(os.FileInfo) (int64, bool) {
	return 0, false
}
