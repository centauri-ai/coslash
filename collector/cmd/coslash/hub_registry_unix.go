//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

func protectHubRegistryDirectory(dir string) error { return os.Chmod(dir, 0o700) }

func syncHubRegistryDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	err = dir.Sync()
	if closeErr := dir.Close(); err == nil {
		err = closeErr
	}
	return err
}

func writeHubRegistryFile(dir string, data []byte) (bool, error) {
	f, err := os.CreateTemp(dir, ".registry-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, "registry.json")); err != nil {
		return false, err
	}
	return true, syncHubRegistryDirectory(dir)
}
