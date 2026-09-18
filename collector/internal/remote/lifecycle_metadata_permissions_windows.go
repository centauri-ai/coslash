package remote

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func readMetadataSequenceContent(path string) ([]byte, error) {
	return readPrivateWindowsFile(path, "metadata sequence")
}

func protectMetadataSequenceDirectory(path string) error {
	return protectPrivateWindowsDirectory(path, "metadata sequence")
}

func protectMetadataSequenceFile(path string, file *os.File) error {
	return protectPrivateWindowsFile(path, file, "metadata sequence")
}

func readPrivateWindowsFile(path, subject string) ([]byte, error) {
	file, err := openPrivateWindowsFile(path, subject)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func openPrivateWindowsFile(path, subject string) (*os.File, error) {
	if err := protectPrivateWindowsDirectory(filepath.Dir(path), subject); err != nil {
		return nil, err
	}
	handle, err := openPrivateWindowsPath(path, windows.GENERIC_READ|windows.READ_CONTROL|windows.WRITE_DAC, false)
	if err != nil {
		return nil, err
	}
	if _, err := privateWindowsFileInformation(handle, subject); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if err := protectPrivateWindowsHandle(handle, false); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("open %s handle", subject)
	}
	return file, nil
}

func protectPrivateWindowsDirectory(path, subject string) error {
	handle, err := openPrivateWindowsPath(path, windows.FILE_LIST_DIRECTORY|windows.READ_CONTROL|windows.WRITE_DAC, true)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("%s directory must not be a reparse point", subject)
	}
	return protectPrivateWindowsHandle(handle, true)
}

func protectPrivateWindowsFile(path string, file *os.File, subject string) error {
	original, err := privateWindowsFileInformation(windows.Handle(file.Fd()), subject)
	if err != nil {
		return err
	}
	handle, err := openPrivateWindowsPath(path, windows.READ_CONTROL|windows.WRITE_DAC, false)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	opened, err := privateWindowsFileInformation(handle, subject)
	if err != nil {
		return err
	}
	if original.VolumeSerialNumber != opened.VolumeSerialNumber || original.FileIndexHigh != opened.FileIndexHigh || original.FileIndexLow != opened.FileIndexLow {
		return fmt.Errorf("%s temporary file changed while securing it", subject)
	}
	return protectPrivateWindowsHandle(handle, false)
}

func openPrivateWindowsPath(path string, access uint32, directory bool) (windows.Handle, error) {
	pointer, err := privateWindowsPathPointer(path)
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

func privateWindowsPathPointer(path string) (*uint16, error) {
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

func privateWindowsFileInformation(handle windows.Handle, subject string) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return info, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return info, fmt.Errorf("%s must be a regular file", subject)
	}
	if info.NumberOfLinks != 1 {
		return info, fmt.Errorf("%s must not be hard linked", subject)
	}
	return info, nil
}

func protectPrivateWindowsHandle(handle windows.Handle, directory bool) error {
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
