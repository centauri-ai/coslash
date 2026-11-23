//go:build !windows

package search

import "os"

func protectDirectory(path string) error { return os.Chmod(path, 0700) }
