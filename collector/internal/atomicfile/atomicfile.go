// Package atomicfile replaces a file so that a reader or a crash sees either
// the old content or the complete new content, never a partial file.
package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Write replaces path with data. It writes a temporary file in the directory
// of path, sets mode, calls protect (when it is not nil) to apply platform
// protection such as a Windows ACL, syncs the file, and renames it over path.
// Then it syncs the directory on a best-effort basis. The directory must exist.
func Write(path string, data []byte, mode fs.FileMode, protect func(tempPath string, file *os.File) error) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	err = file.Chmod(mode)
	if err == nil && protect != nil {
		err = protect(tempPath, file)
	}
	if err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	_ = SyncDir(filepath.Dir(path))
	return nil
}

// SyncDir flushes the directory entry of a completed rename. Windows has no
// supported flush for a directory handle, so it does nothing there.
func SyncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
