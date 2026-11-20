//go:build !windows

package session

import "os"

func protectLocationFile(_ string, f *os.File) error { return f.Chmod(0600) }
