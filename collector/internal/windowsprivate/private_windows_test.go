//go:build windows

package windowsprivate

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

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
	originalGet, originalSet, originalSetNamed, originalIdentity := getSecurityInfo, setSecurityInfo, setNamedSecurityInfo, currentIdentity
	t.Cleanup(func() {
		getSecurityInfo, setSecurityInfo, setNamedSecurityInfo, currentIdentity = originalGet, originalSet, originalSetNamed, originalIdentity
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
	var calls []windows.SECURITY_INFORMATION
	setNamedSecurityInfo = func(path string, _ windows.SE_OBJECT_TYPE, information windows.SECURITY_INFORMATION, owner, _ *windows.SID, _ *windows.ACL, _ *windows.ACL) error {
		calls = append(calls, information)
		if path != `C:\private-state` || owner == nil || !owner.Equals(user.User.Sid) {
			t.Fatalf("owner = %v, want current user %s", owner, user.User.Sid)
		}
		claimed = true
		return nil
	}
	setSecurityInfo = func(_ windows.Handle, _ windows.SE_OBJECT_TYPE, information windows.SECURITY_INFORMATION, _, _ *windows.SID, _ *windows.ACL, _ *windows.ACL) error {
		calls = append(calls, information)
		return nil
	}

	if err := protectHandle(0, `C:\private-state`, "private state", false); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != windows.OWNER_SECURITY_INFORMATION || calls[1] != windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION {
		t.Fatalf("security updates = %#v, want owner then protected DACL", calls)
	}
}
