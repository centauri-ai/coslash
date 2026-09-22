//go:build !windows

package synthesis

import (
	"fmt"
	"io"
	"os"
)

func protectSynthesisDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func protectSynthesisFile(_ string, file *os.File) error {
	return file.Chmod(0o600)
}

func readSynthesisFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("synthesis cache must be a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func readSynthesisFileInProtectedDirectory(path string) ([]byte, error) {
	return readSynthesisFile(path)
}
