//go:build !windows

package settings

import (
	"fmt"
	"os"
)

func readSettingsFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("settings.json must be a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("settings.json permissions must be 0600")
	}
	return os.ReadFile(path)
}

func protectSettingsDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func protectSettingsFile(_ string, file *os.File) error {
	return file.Chmod(0o600)
}
