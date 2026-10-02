//go:build windows

package opencode

import (
	"fmt"
	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
	"golang.org/x/sys/windows"
	"os"
)

func deletionFileIdentity(_ os.FileInfo, path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func syncDeletionDirectory(string) error { return nil }

func deletionJSONInfo(path string) (os.FileInfo, error) {
	file, err := openDeletionJSON(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.Stat()
}

func openDeletionJSON(path string) (*os.File, error) {
	handle, err := windowsprivate.Open(path, windows.GENERIC_READ, false)
	if err != nil {
		return nil, err
	}
	kind, err := windows.GetFileType(handle)
	if err == nil && kind == windows.FILE_TYPE_DISK {
		_, err = windowsprivate.FileInformation(handle, "OpenCode deletion metadata")
	} else if err == nil {
		err = ErrSessionUnverified
	}
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
