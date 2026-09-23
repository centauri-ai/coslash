//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

func protectTokenDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func writeTokenFile(home, token string) error {
	temporary, err := os.CreateTemp(home, ".token-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(token + "\n"); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(home, "token"))
}
