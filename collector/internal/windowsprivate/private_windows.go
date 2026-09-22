//go:build windows

package windowsprivate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var (
	getSecurityInfo      = windows.GetSecurityInfo
	setSecurityInfo      = windows.SetSecurityInfo
	setNamedSecurityInfo = windows.SetNamedSecurityInfo
	currentIdentity      = loadCurrentIdentity
)

func ReadFile(path, subject, directorySubject string) ([]byte, error) {
	file, err := OpenFile(path, subject, directorySubject)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func OpenFile(path, subject, directorySubject string) (*os.File, error) {
	if err := ProtectDirectory(filepath.Dir(path), directorySubject); err != nil {
		return nil, err
	}
	handle, err := Open(path, windows.GENERIC_READ|windows.READ_CONTROL|windows.WRITE_DAC, false)
	if err != nil {
		return nil, err
	}
	if _, err := FileInformation(handle, subject); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if err := protectHandle(handle, path, subject, false); err != nil {
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

func ProtectDirectory(path, subject string) error {
	handle, err := Open(path, windows.FILE_LIST_DIRECTORY|windows.READ_CONTROL|windows.WRITE_DAC, true)
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
	return protectHandle(handle, path, subject+" directory", true)
}

func ProtectFile(path string, file *os.File, subject string) error {
	original, err := FileInformation(windows.Handle(file.Fd()), subject)
	if err != nil {
		return err
	}
	handle, err := Open(path, windows.READ_CONTROL|windows.WRITE_DAC, false)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	opened, err := FileInformation(handle, subject)
	if err != nil {
		return err
	}
	if !SameFile(original, opened) {
		return fmt.Errorf("%s temporary file changed while securing it", subject)
	}
	return protectHandle(handle, path, subject, false)
}

func Open(path string, access uint32, directory bool) (windows.Handle, error) {
	pointer, err := PathPointer(path)
	if err != nil {
		return 0, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	return windows.CreateFile(pointer, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, flags, 0)
}

func PathPointer(path string) (*uint16, error) {
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

func FileInformation(handle windows.Handle, subject string) (windows.ByHandleFileInformation, error) {
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

func SameFile(left, right windows.ByHandleFileInformation) bool {
	return left.VolumeSerialNumber == right.VolumeSerialNumber && left.FileIndexHigh == right.FileIndexHigh && left.FileIndexLow == right.FileIndexLow
}

func protectHandle(handle windows.Handle, path, subject string, directory bool) error {
	user, administrator, err := currentIdentity()
	if err != nil {
		return err
	}
	descriptor, err := getSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	if owner == nil {
		return fmt.Errorf("%s must be owned by the current user", subject)
	}
	if !owner.Equals(user) {
		if !administrator || !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return fmt.Errorf("%s must be owned by the current user", subject)
		}
		if err := setNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user, nil, nil, nil); err != nil {
			return fmt.Errorf("establish current-user ownership for %s: %w", subject, err)
		}
		descriptor, err = getSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		owner, _, err = descriptor.Owner()
		if err != nil {
			return err
		}
		if owner == nil || !owner.Equals(user) {
			return fmt.Errorf("%s ownership changed before it could be secured", subject)
		}
	}
	inheritance := ""
	if directory {
		inheritance = "OICI"
	}
	sddl := "D:P(A;" + inheritance + ";FA;;;" + user.String() + ")" +
		"(A;" + inheritance + ";FA;;;SY)(A;" + inheritance + ";FA;;;BA)"
	descriptor, err = windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return setSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func loadCurrentIdentity() (*windows.SID, bool, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		return nil, false, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, false, err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, false, err
	}
	var impersonation windows.Token
	if err := windows.DuplicateTokenEx(token, windows.TOKEN_QUERY, nil, windows.SecurityIdentification, windows.TokenImpersonation, &impersonation); err != nil {
		return nil, false, err
	}
	defer impersonation.Close()
	isAdministrator, err := impersonation.IsMember(administrators)
	if err != nil {
		return nil, false, err
	}
	return user.User.Sid, isAdministrator, nil
}
