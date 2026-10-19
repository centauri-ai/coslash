//go:build !windows

package syncv4

import (
	"errors"
	"os"
)

func protectQueueDirectory(path string) error        { return os.Chmod(path, 0o700) }
func protectQueueFile(_ string, file *os.File) error { return file.Chmod(0o600) }

func readQueueFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("v4 queue must be a private regular file")
	}
	return os.ReadFile(path)
}
