//go:build !windows

package grokcli

import "os"

func protectDirectory(path string) error { return os.Chmod(path, 0o700) }
