//go:build windows

package main

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// retireLegacyBackup removes the legacy helper's only backup without letting
// the published 0.4.2 helper roll back concurrently.
//
// That helper restores with MoveFileEx(backup -> target) and has no
// completion signal after it releases update.lock. A plain DeleteFile can mark
// the very file object that MoveFileEx is renaming over the target
// delete-pending, so the target disappears when handles close. Here the backup
// is opened with DELETE access and no sharing, so the helper's rename of it
// (and any delete) fails with a sharing violation while it is held. verify
// runs under that hold, so it sees an installed version no rollback can
// change; only then is the file marked for deletion. If the helper's rename
// opened the backup first, the open here fails and the caller retries, and the
// next version check reports the rollback.
func retireLegacyBackup(backup string, verify func() error) error {
	name, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.DELETE|windows.FILE_READ_ATTRIBUTES, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return verify()
		}
		return err
	}
	defer windows.CloseHandle(handle)
	if err := verify(); err != nil {
		return err
	}
	disposition := struct{ DeleteFile byte }{DeleteFile: 1}
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&disposition)), uint32(unsafe.Sizeof(disposition))); err != nil {
		return err
	}
	return nil
}
