package settings

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func readSettingsFile(path string) ([]byte, error) {
	if err := protectWindowsDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("secure settings directory: %w", err)
	}
	pointer, err := windowsPathPointer(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ|windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	if err := validateWindowsFileHandle(handle); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if err := protectWindowsHandle(handle, false); err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("secure settings.json: %w", err)
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("open settings.json handle")
	}
	defer file.Close()
	return io.ReadAll(file)
}

func protectSettingsDirectory(path string) error {
	return protectWindowsDirectory(path)
}

func protectSettingsFile(path string, file *os.File) error {
	original := windows.Handle(file.Fd())
	originalInfo, err := windowsFileInformation(original)
	if err != nil {
		return err
	}
	pointer, err := windowsPathPointer(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	openedInfo, err := windowsFileInformation(handle)
	if err != nil {
		return err
	}
	if !sameWindowsFile(originalInfo, openedInfo) {
		return fmt.Errorf("settings temporary file changed while securing it")
	}
	return protectWindowsHandle(handle, false)
}

func protectWindowsDirectory(path string) error {
	pointer, err := windowsPathPointer(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_LIST_DIRECTORY|windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("settings directory must not be a reparse point")
	}
	return protectWindowsHandle(handle, true)
}

func windowsPathPointer(path string) (*uint16, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absolute, `\\?\`) {
		if strings.HasPrefix(absolute, `\\`) {
			absolute = `\\?\UNC\` + strings.TrimPrefix(absolute, `\\`)
		} else {
			absolute = `\\?\` + absolute
		}
	}
	return windows.UTF16PtrFromString(absolute)
}

func validateWindowsFileHandle(handle windows.Handle) error {
	_, err := windowsFileInformation(handle)
	return err
}

func windowsFileInformation(handle windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return info, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return info, fmt.Errorf("settings.json must be a regular file")
	}
	if info.NumberOfLinks != 1 {
		return info, fmt.Errorf("settings.json must not be hard linked")
	}
	return info, nil
}

func sameWindowsFile(left, right windows.ByHandleFileInformation) bool {
	return left.VolumeSerialNumber == right.VolumeSerialNumber &&
		left.FileIndexHigh == right.FileIndexHigh &&
		left.FileIndexLow == right.FileIndexLow
}

func protectWindowsHandle(handle windows.Handle, directory bool) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	inheritance := ""
	if directory {
		inheritance = "OICI"
	}
	sddl := "D:P(A;" + inheritance + ";FA;;;" + user.User.Sid.String() + ")" +
		"(A;" + inheritance + ";FA;;;SY)(A;" + inheritance + ";FA;;;BA)"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}
