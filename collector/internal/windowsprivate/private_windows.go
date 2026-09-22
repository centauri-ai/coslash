//go:build windows

package windowsprivate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type PendingFile struct {
	File      *os.File
	directory windows.Handle
	committed bool
}

type fileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

var (
	getSecurityInfo     = windows.GetSecurityInfo
	setSecurityInfo     = windows.SetSecurityInfo
	currentIdentity     = loadCurrentIdentity
	openOwnershipHandle = reopenOwnershipHandle
	openObject          = Open
	getFileInformation  = windows.GetFileInformationByHandle
	closeHandle         = windows.CloseHandle
)

func ReadFile(path, subject, directorySubject string) ([]byte, error) {
	file, err := OpenFile(path, subject, directorySubject)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func ReadFileInProtectedDirectory(path, subject string) ([]byte, error) {
	file, err := openFile(path, subject)
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
	return openFile(path, subject)
}

func openFile(path, subject string) (*os.File, error) {
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

func CreatePrivateTempFile(directoryPath, prefix, subject string) (*PendingFile, error) {
	if prefix == "" || filepath.Base(prefix) != prefix {
		return nil, fmt.Errorf("invalid %s temporary file prefix", subject)
	}
	directory, err := Open(directoryPath, windows.FILE_LIST_DIRECTORY|windows.FILE_WRITE_DATA|windows.READ_CONTROL|windows.WRITE_DAC, true)
	if err != nil {
		return nil, err
	}
	keepDirectory := false
	defer func() {
		if !keepDirectory {
			windows.CloseHandle(directory)
		}
	}()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(directory, &info); err != nil {
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return nil, fmt.Errorf("%s directory must not be a reparse point", subject)
	}
	if err := protectHandle(directory, directoryPath, subject+" directory", true); err != nil {
		return nil, err
	}
	user, _, err := currentIdentity()
	if err != nil {
		return nil, err
	}
	descriptor, err := privateSecurityDescriptor(user, false)
	if err != nil {
		return nil, err
	}
	for range 100 {
		name, err := randomPrivateName(prefix)
		if err != nil {
			return nil, err
		}
		objectName, err := windows.NewNTUnicodeString(name)
		if err != nil {
			return nil, err
		}
		attributes := &windows.OBJECT_ATTRIBUTES{
			Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
			RootDirectory:      directory,
			ObjectName:         objectName,
			Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
			SecurityDescriptor: descriptor,
		}
		var handle windows.Handle
		var status windows.IO_STATUS_BLOCK
		err = windows.NtCreateFile(
			&handle,
			windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE,
			attributes,
			&status,
			nil,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
			windows.FILE_CREATE,
			windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			0,
			0,
		)
		if err == windows.STATUS_OBJECT_NAME_COLLISION {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("create private %s: %w", subject, err)
		}
		file := os.NewFile(uintptr(handle), filepath.Join(directoryPath, name))
		if file == nil {
			windows.CloseHandle(handle)
			return nil, fmt.Errorf("create private %s file handle", subject)
		}
		keepDirectory = true
		return &PendingFile{File: file, directory: directory}, nil
	}
	return nil, fmt.Errorf("create private %s: temporary name collisions", subject)
}

func (file *PendingFile) Commit(name string) error {
	if file == nil || file.File == nil || file.directory == 0 {
		return errors.New("commit private file: file is closed")
	}
	if name == "" || filepath.Base(name) != name {
		return errors.New("commit private file: invalid destination name")
	}
	if err := file.File.Sync(); err != nil {
		return err
	}
	nameUTF16, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	nameLength := len(nameUTF16) - 1
	var layout fileRenameInformation
	buffer := make([]byte, int(unsafe.Offsetof(layout.FileName))+nameLength*2)
	rename := (*fileRenameInformation)(unsafe.Pointer(&buffer[0]))
	rename.ReplaceIfExists = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	rename.RootDirectory = file.directory
	rename.FileNameLength = uint32(nameLength * 2)
	copy(unsafe.Slice(&rename.FileName[0], nameLength), nameUTF16[:nameLength])
	var status windows.IO_STATUS_BLOCK
	if err := windows.NtSetInformationFile(windows.Handle(file.File.Fd()), &status, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation); err != nil {
		return err
	}
	file.committed = true
	return nil
}

func (file *PendingFile) Close() error {
	if file == nil {
		return nil
	}
	var disposeErr error
	if file.File != nil && !file.committed {
		var status windows.IO_STATUS_BLOCK
		disposition := byte(1)
		disposeErr = windows.NtSetInformationFile(windows.Handle(file.File.Fd()), &status, &disposition, 1, windows.FileDispositionInformation)
	}
	var fileErr error
	if file.File != nil {
		fileErr = file.File.Close()
		file.File = nil
	}
	var directoryErr error
	if file.directory != 0 {
		directoryErr = windows.CloseHandle(file.directory)
		file.directory = 0
	}
	return errors.Join(disposeErr, fileErr, directoryErr)
}

func randomPrivateName(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(random[:]), nil
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
		ownershipHandle, err := openOwnershipHandle(handle, path, directory)
		if err != nil {
			return fmt.Errorf("reopen %s for ownership: %w", subject, err)
		}
		defer closeHandle(ownershipHandle)
		if err := setSecurityInfo(ownershipHandle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user, nil, nil, nil); err != nil {
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
	descriptor, err = privateSecurityDescriptor(user, directory)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return setSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func privateSecurityDescriptor(user *windows.SID, directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	inheritance := ""
	if directory {
		inheritance = "OICI"
	}
	sddl := "O:" + user.String() + "D:P(A;" + inheritance + ";FA;;;" + user.String() + ")" +
		"(A;" + inheritance + ";FA;;;SY)(A;" + inheritance + ";FA;;;BA)"
	return windows.SecurityDescriptorFromString(sddl)
}

func reopenOwnershipHandle(original windows.Handle, path string, directory bool) (windows.Handle, error) {
	handle, err := openObject(path, windows.READ_CONTROL|windows.WRITE_OWNER, directory)
	if err != nil {
		return 0, err
	}
	var originalInfo, openedInfo windows.ByHandleFileInformation
	if err := getFileInformation(original, &originalInfo); err != nil {
		closeHandle(handle)
		return 0, err
	}
	if err := getFileInformation(handle, &openedInfo); err != nil {
		closeHandle(handle)
		return 0, err
	}
	if !SameFile(originalInfo, openedInfo) {
		closeHandle(handle)
		return 0, fmt.Errorf("object changed while establishing ownership")
	}
	return handle, nil
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
