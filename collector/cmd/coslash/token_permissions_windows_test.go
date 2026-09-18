package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWriteTokenSupportsLongUnicodeHome(t *testing.T) {
	home := t.TempDir()
	for index := 0; len(utf16.Encode([]rune(home))) <= 300; index++ {
		home = filepath.Join(home, fmt.Sprintf("令牌-%02d-%s", index, strings.Repeat("长", 24)))
	}
	t.Setenv("COSLASH_HOME", home)
	if err := writeToken("secret"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "token")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != "secret" {
		t.Fatalf("token content = %q", content)
	}
	assertPrivateTokenPath(t, home, true)
	assertPrivateTokenPath(t, path, false)
}

func TestWriteTokenRejectsReparseHome(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-home")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	t.Setenv("COSLASH_HOME", link)
	if err := writeToken("secret"); err == nil || !strings.Contains(err.Error(), "must not be a reparse point") {
		t.Fatalf("reparse home error = %v", err)
	}
}

func assertPrivateTokenPath(t *testing.T, path string, directory bool) {
	t.Helper()
	handle, err := openTokenPath(path, windows.READ_CONTROL, directory)
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
		wantFlags := uint8(0)
		if directory {
			wantFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != wantFlags || ace.Mask != 0x1f01ff {
			t.Fatalf("unexpected ACE %d: %s", index, descriptor.String())
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
