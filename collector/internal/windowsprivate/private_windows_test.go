//go:build windows

package windowsprivate

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReopenOwnershipHandleRejectsReplacement(t *testing.T) {
	originalOpen, originalInformation, originalClose := openObject, getFileInformation, closeHandle
	t.Cleanup(func() { openObject, getFileInformation, closeHandle = originalOpen, originalInformation, originalClose })
	const original, replacement windows.Handle = 41, 42
	openObject = func(path string, access uint32, directory bool) (windows.Handle, error) {
		if path != `C:\private-state` || access != windows.READ_CONTROL|windows.WRITE_OWNER || directory {
			t.Fatalf("Open(%q, %#x, %t)", path, access, directory)
		}
		return replacement, nil
	}
	getFileInformation = func(handle windows.Handle, info *windows.ByHandleFileInformation) error {
		info.VolumeSerialNumber = 1
		info.FileIndexLow = uint32(handle)
		return nil
	}
	closed := false
	closeHandle = func(handle windows.Handle) error {
		closed = handle == replacement
		return nil
	}

	handle, err := reopenOwnershipHandle(original, `C:\private-state`, false)
	if err == nil {
		closeHandle(handle)
		t.Fatal("reopenOwnershipHandle() accepted a replacement path")
	}
	if !strings.Contains(err.Error(), "object changed") {
		t.Fatalf("reopenOwnershipHandle() error = %v", err)
	}
	if !closed {
		t.Fatal("replacement handle was not closed")
	}
}

func TestProtectHandleRejectsForeignOwner(t *testing.T) {
	originalGet, originalIdentity := getSecurityInfo, currentIdentity
	t.Cleanup(func() { getSecurityInfo, currentIdentity = originalGet, originalIdentity })

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner := "SY"
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		owner = "BA"
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:" + owner)
	if err != nil {
		t.Fatal(err)
	}
	getSecurityInfo = func(windows.Handle, windows.SE_OBJECT_TYPE, windows.SECURITY_INFORMATION) (*windows.SECURITY_DESCRIPTOR, error) {
		return descriptor, nil
	}
	currentIdentity = func() (*windows.SID, bool, error) { return user.User.Sid, false, nil }

	err = protectHandle(0, `C:\private-state`, "private state", false)
	if err == nil || !strings.Contains(err.Error(), "must be owned by the current user") {
		t.Fatalf("protectHandle() error = %v", err)
	}
}

func TestProtectHandleClaimsAdministratorsOwnedState(t *testing.T) {
	originalGet, originalSet, originalIdentity, originalOpen, originalClose := getSecurityInfo, setSecurityInfo, currentIdentity, openOwnershipHandle, closeHandle
	t.Cleanup(func() {
		getSecurityInfo, setSecurityInfo, currentIdentity, openOwnershipHandle, closeHandle = originalGet, originalSet, originalIdentity, originalOpen, originalClose
	})

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:BA")
	if err != nil {
		t.Fatal(err)
	}
	claimed := false
	getSecurityInfo = func(windows.Handle, windows.SE_OBJECT_TYPE, windows.SECURITY_INFORMATION) (*windows.SECURITY_DESCRIPTOR, error) {
		if claimed {
			return windows.SecurityDescriptorFromString("O:" + user.User.Sid.String())
		}
		return descriptor, nil
	}
	currentIdentity = func() (*windows.SID, bool, error) { return user.User.Sid, true, nil }
	const ownershipHandle windows.Handle = 42
	openOwnershipHandle = func(handle windows.Handle, path string, directory bool) (windows.Handle, error) {
		if handle != 0 || path != `C:\private-state` || directory {
			t.Fatalf("openOwnershipHandle(%v, %q, %t)", handle, path, directory)
		}
		return ownershipHandle, nil
	}
	closeHandle = func(handle windows.Handle) error {
		if handle != ownershipHandle {
			t.Fatalf("CloseHandle(%v), want %v", handle, ownershipHandle)
		}
		return nil
	}
	var calls []windows.SECURITY_INFORMATION
	setSecurityInfo = func(handle windows.Handle, _ windows.SE_OBJECT_TYPE, information windows.SECURITY_INFORMATION, owner, _ *windows.SID, _ *windows.ACL, _ *windows.ACL) error {
		calls = append(calls, information)
		if information == windows.OWNER_SECURITY_INFORMATION {
			if handle != ownershipHandle || owner == nil || !owner.Equals(user.User.Sid) {
				t.Fatalf("ownership update handle = %v, owner = %v", handle, owner)
			}
			claimed = true
		}
		return nil
	}

	if err := protectHandle(0, `C:\private-state`, "private state", false); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != windows.OWNER_SECURITY_INFORMATION || calls[1] != windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION {
		t.Fatalf("security updates = %#v, want owner then protected DACL", calls)
	}
}
