//go:build !windows

package search

import "os"

func protectDirectory(path string) error     { return os.Chmod(path, 0700) }
func protectFile(_ string, f *os.File) error { return f.Chmod(0600) }
