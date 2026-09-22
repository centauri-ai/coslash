//go:build !windows

package remote

import (
	"fmt"
	"os"
)

func readMetadataSequenceContent(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: invalid local sequence state", ErrHelperMetadata)
	}
	return os.ReadFile(path)
}

func protectMetadataSequenceDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func protectMetadataSequenceFile(_ string, file *os.File) error {
	return file.Chmod(0o600)
}
