//go:build windows

package remote

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func setWindowsDACL(t *testing.T, path, sddl string, directory bool) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if directory {
		information |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, information, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func assertPrivateWindowsACL(t *testing.T, path string, directory bool) {
	t.Helper()
	handle, err := openPrivateWindowsPath(path, windows.READ_CONTROL, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("DACL is not protected: %s", descriptor.String())
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		user.User.Sid.String(): false,
		"S-1-5-18":             false,
		"S-1-5-32-544":         false,
	}
	if dacl.AceCount != uint16(len(want)) {
		t.Fatalf("ACE count = %d, want %d: %s", dacl.AceCount, len(want), descriptor.String())
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("ACE %d type = %d, want allowed: %s", index, ace.Header.AceType, descriptor.String())
		}
		wantFlags := uint8(0)
		if directory {
			wantFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
		}
		if ace.Header.AceFlags != wantFlags {
			t.Fatalf("ACE %d flags = %#x, want %#x: %s", index, ace.Header.AceFlags, wantFlags, descriptor.String())
		}
		const fileAllAccess = 0x1f01ff
		if ace.Mask != fileAllAccess {
			t.Fatalf("ACE %d mask = %#x, want %#x: %s", index, ace.Mask, fileAllAccess, descriptor.String())
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if _, ok := want[sid]; !ok {
			t.Fatalf("unexpected trustee %s: %s", sid, descriptor.String())
		}
		want[sid] = true
	}
	for sid, found := range want {
		if !found {
			t.Fatalf("missing trustee %s: %s", sid, descriptor.String())
		}
	}
}
