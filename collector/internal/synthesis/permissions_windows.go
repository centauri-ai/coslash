package synthesis

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func protectSynthesisDirectory(path string) error {
	handle, err := openSynthesisPath(path, windows.FILE_LIST_DIRECTORY|windows.READ_CONTROL|windows.WRITE_DAC, true)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("synthesis directory must not be a reparse point")
	}
	return protectSynthesisHandle(handle, true)
}

func protectSynthesisFile(path string, file *os.File) error {
	original, err := synthesisFileInformation(windows.Handle(file.Fd()))
	if err != nil {
		return err
	}
	handle, err := openSynthesisPath(path, windows.READ_CONTROL|windows.WRITE_DAC, false)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	opened, err := synthesisFileInformation(handle)
	if err != nil {
		return err
	}
	if original.VolumeSerialNumber != opened.VolumeSerialNumber || original.FileIndexHigh != opened.FileIndexHigh || original.FileIndexLow != opened.FileIndexLow {
		return fmt.Errorf("synthesis temporary file changed while securing it")
	}
	return protectSynthesisHandle(handle, false)
}

func readSynthesisFile(path string) ([]byte, error) {
	if err := protectSynthesisDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	handle, err := openSynthesisPath(path, windows.GENERIC_READ|windows.READ_CONTROL|windows.WRITE_DAC, false)
	if err != nil {
		return nil, err
	}
	if _, err := synthesisFileInformation(handle); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if err := protectSynthesisHandle(handle, false); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("open synthesis cache handle")
	}
	defer file.Close()
	return io.ReadAll(file)
}

func openSynthesisPath(path string, access uint32, directory bool) (windows.Handle, error) {
	pointer, err := synthesisPathPointer(path)
	if err != nil {
		return 0, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	return windows.CreateFile(pointer, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, flags, 0)
}

func synthesisPathPointer(path string) (*uint16, error) {
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

func synthesisFileInformation(handle windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return info, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return info, fmt.Errorf("synthesis cache must be a regular file")
	}
	if info.NumberOfLinks != 1 {
		return info, fmt.Errorf("synthesis cache must not be hard linked")
	}
	return info, nil
}

func protectSynthesisHandle(handle windows.Handle, directory bool) error {
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
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
