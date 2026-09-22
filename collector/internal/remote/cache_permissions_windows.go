package remote

import (
	"os"
	"path/filepath"
)

func readCacheFile(path string) ([]byte, error) {
	if err := protectCacheParents(path); err != nil {
		return nil, err
	}
	return readPrivateWindowsFile(path, "remote cache")
}

func openCacheFile(path string) (*os.File, error) {
	if err := protectCacheParents(path); err != nil {
		return nil, err
	}
	return openPrivateWindowsFile(path, "remote cache")
}

func protectCacheParents(path string) error {
	directory := filepath.Dir(path)
	if err := protectPrivateWindowsDirectory(filepath.Dir(directory), "remote cache"); err != nil {
		return err
	}
	return protectPrivateWindowsDirectory(directory, "remote cache")
}

func protectCacheDirectory(path string) error {
	return protectPrivateWindowsDirectory(path, "remote cache")
}

func protectCacheFile(path string, file *os.File) error {
	return protectPrivateWindowsFile(path, file, "remote cache")
}
