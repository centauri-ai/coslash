//go:build windows

package opencode

import (
	"path/filepath"
	"strings"
)

func sameDirectory(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
