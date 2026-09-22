//go:build !windows

package opencode

import "path/filepath"

func sameDirectory(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
