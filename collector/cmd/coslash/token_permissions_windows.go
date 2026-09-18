package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func protectTokenDirectory(path string) error {
	handle, err := openTokenPath(path, windows.FILE_LIST_DIRECTORY|windows.READ_CONTROL|windows.WRITE_DAC, true)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("token directory must not be a reparse point")
	}
	return protectTokenHandle(handle, true)
}

func protectTokenFile(path string, file *os.File) error {
	original, err := tokenFileInformation(windows.Handle(file.Fd()))
	if err != nil {
		return err
	}
	handle, err := openTokenPath(path, windows.READ_CONTROL|windows.WRITE_DAC, false)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	opened, err := tokenFileInformation(handle)
	if err != nil {
		return err
	}
	if original.VolumeSerialNumber != opened.VolumeSerialNumber || original.FileIndexHigh != opened.FileIndexHigh || original.FileIndexLow != opened.FileIndexLow {
		return fmt.Errorf("token temporary file changed while securing it")
	}
	return protectTokenHandle(handle, false)
}

func openTokenPath(path string, access uint32, directory bool) (windows.Handle, error) {
	pointer, err := tokenPathPointer(path)
	if err != nil {
		return 0, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	return windows.CreateFile(
		pointer,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
}

func tokenPathPointer(path string) (*uint16, error) {
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

func tokenFileInformation(handle windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return info, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return info, fmt.Errorf("token must be a regular file")
	}
	if info.NumberOfLinks != 1 {
		return info, fmt.Errorf("token must not be hard linked")
	}
	return info, nil
}

func protectTokenHandle(handle windows.Handle, directory bool) error {
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
