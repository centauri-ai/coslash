package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsSettingsPersistAcrossRestartWithPrivateACL(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	store := Open()
	if err := store.Save(Defaults()); err != nil {
		t.Fatal(err)
	}

	reopened := Open().State()
	if !reopened.Valid || !reopened.Persisted || reopened.Error != "" {
		t.Fatalf("reopened settings = %+v", reopened)
	}
	assertPrivateWindowsACL(t, Home(), true)
	assertPrivateWindowsACL(t, Path(), false)
}

func TestWindowsSettingsPersistInLongUnicodePath(t *testing.T) {
	home := t.TempDir()
	for index := 0; len(utf16.Encode([]rune(home))) <= 300; index++ {
		home = filepath.Join(home, fmt.Sprintf("设置目录-%02d-%s", index, strings.Repeat("长", 24)))
	}
	t.Setenv("COSLASH_HOME", home)
	store := Open()
	if err := store.Save(Defaults()); err != nil {
		t.Fatal(err)
	}

	reopened := Open().State()
	if !reopened.Valid || !reopened.Persisted || reopened.Error != "" {
		t.Fatalf("reopened long-path settings = %+v", reopened)
	}
	assertPrivateWindowsACL(t, Home(), true)
	assertPrivateWindowsACL(t, Path(), false)
}

func TestWindowsSettingsLoadRepairsLegacyBroadACLs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	data, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	setWindowsDACL(t, home, "D:(A;OICI;FA;;;WD)", true)
	setWindowsDACL(t, Path(), "D:(A;;FA;;;WD)", false)

	state := Open().State()
	if !state.Valid || !state.Persisted || state.Error != "" {
		t.Fatalf("migrated settings = %+v", state)
	}
	assertPrivateWindowsACL(t, home, true)
	assertPrivateWindowsACL(t, Path(), false)
}

func TestWindowsSettingsRejectHardLinkedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	data, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(Path(), filepath.Join(home, "settings-copy.json")); err != nil {
		t.Fatal(err)
	}

	state := Open().State()
	if state.Valid || !strings.Contains(state.Error, "must not be hard linked") {
		t.Fatalf("hard-linked settings = %+v", state)
	}
}

func TestWindowsSettingsRejectReparseHome(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-home")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	t.Setenv("COSLASH_HOME", link)

	state := Open().State()
	if state.Valid || !strings.Contains(state.Error, "must not be a reparse point") {
		t.Fatalf("reparse settings home = %+v", state)
	}
}

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
	pointer, err := windowsPathPointer(path)
	if err != nil {
		t.Fatal(err)
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
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
			t.Fatalf("ACE %d mask = %#x, want full file access %#x: %s", index, ace.Mask, fileAllAccess, descriptor.String())
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
