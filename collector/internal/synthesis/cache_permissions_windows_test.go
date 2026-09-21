package synthesis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"golang.org/x/sys/windows"
)

func TestSynthesisCacheRepairsLegacyBroadACLs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	cache := NewCache()
	record := Record{Revision: 42, Synthesis: session.SessionSynthesis{Outcome: "shipped"}}
	if err := cache.Store("session", record); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(SummariesDir(), "session.json")
	setSynthesisWindowsDACL(t, home, "D:(A;OICI;FA;;;WD)", true)
	setSynthesisWindowsDACL(t, SummariesDir(), "D:(A;OICI;FA;;;WD)", true)
	setSynthesisWindowsDACL(t, path, "D:(A;;FA;;;WD)", false)

	if _, err := NewCache().Load("session"); err != nil {
		t.Fatal(err)
	}
	assertPrivateSynthesisPath(t, home, true)
	assertPrivateSynthesisPath(t, SummariesDir(), true)
	assertPrivateSynthesisPath(t, path, false)
}

func TestSynthesisCacheRejectsHardLinkedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := NewCache().Store("session", Record{Revision: 42}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(SummariesDir(), "session.json")
	if err := os.Link(path, filepath.Join(SummariesDir(), "session-copy.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCache().Load("session"); err == nil || !strings.Contains(err.Error(), "must not be hard linked") {
		t.Fatalf("hard-linked cache error = %v", err)
	}
}

func TestSynthesisCacheSupportsLongUnicodeHome(t *testing.T) {
	home := t.TempDir()
	for index := 0; len(utf16.Encode([]rune(home))) <= 300; index++ {
		home = filepath.Join(home, fmt.Sprintf("摘要-%02d-%s", index, strings.Repeat("长", 24)))
	}
	t.Setenv("COSLASH_HOME", home)
	if err := NewCache().Store("session", Record{Revision: 42}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCache().Load("session"); err != nil {
		t.Fatal(err)
	}
	assertPrivateSynthesisPath(t, home, true)
	assertPrivateSynthesisPath(t, SummariesDir(), true)
	assertPrivateSynthesisPath(t, filepath.Join(SummariesDir(), "session.json"), false)
}

func TestSynthesisCacheRejectsReparseHome(t *testing.T) {
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
	if err := NewCache().Store("session", Record{Revision: 42}); err == nil || !strings.Contains(err.Error(), "must not be a reparse point") {
		t.Fatalf("reparse home error = %v", err)
	}
}

func setSynthesisWindowsDACL(t *testing.T, path, sddl string, directory bool) {
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
