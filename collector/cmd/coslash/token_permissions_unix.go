//go:build !windows

package main

import "os"

func protectTokenDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func protectTokenFile(_ string, file *os.File) error {
	return file.Chmod(0o600)
}
