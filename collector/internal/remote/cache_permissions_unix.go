//go:build !windows

package remote

import (
	"fmt"
	"io"
	"os"
)

func readCacheFile(path string) ([]byte, error) {
	file, err := openCacheFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func openCacheFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("remote cache must be a private regular file")
	}
	return os.Open(path)
}

func protectCacheDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func protectCacheFile(_ string, file *os.File) error {
	return file.Chmod(0o600)
}
