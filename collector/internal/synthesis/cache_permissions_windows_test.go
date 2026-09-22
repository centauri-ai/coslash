package synthesis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/windowstest"
)

func TestSynthesisCacheRepairsLegacyBroadACLs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	cache := NewCache()
	record := Record{Revision: 42, Synthesis: session.SessionSynthesis{Outcome: "shipped"}}
	if err := cache.Store("codex", "session", record); err != nil {
		t.Fatal(err)
	}
	agentDirectory := filepath.Join(SummariesDir(), "codex")
	path := filepath.Join(agentDirectory, "session.json")
	windowstest.SetDACL(t, home, "D:(A;OICI;FA;;;WD)", true)
	windowstest.SetDACL(t, SummariesDir(), "D:(A;OICI;FA;;;WD)", true)
	windowstest.SetDACL(t, agentDirectory, "D:(A;OICI;FA;;;WD)", true)
	windowstest.SetDACL(t, path, "D:(A;;FA;;;WD)", false)

	if _, err := NewCache().Load("codex", "session"); err != nil {
		t.Fatal(err)
	}
	windowstest.AssertPrivateACL(t, home, true)
	windowstest.AssertPrivateACL(t, SummariesDir(), true)
	windowstest.AssertPrivateACL(t, agentDirectory, true)
	windowstest.AssertPrivateACL(t, path, false)
}

func TestSynthesisCacheRejectsHardLinkedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := NewCache().Store("codex", "session", Record{Revision: 42}); err != nil {
		t.Fatal(err)
	}
	agentDirectory := filepath.Join(SummariesDir(), "codex")
	path := filepath.Join(agentDirectory, "session.json")
	if err := os.Link(path, filepath.Join(agentDirectory, "session-copy.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCache().Load("codex", "session"); err == nil || !strings.Contains(err.Error(), "must not be hard linked") {
		t.Fatalf("hard-linked cache error = %v", err)
	}
}

func TestMigrateLegacyCacheRejectsHardLinkedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	legacy := writeLegacyRecord(t, "session")
	if err := os.Link(legacy, filepath.Join(SummariesDir(), "session-copy.json")); err != nil {
		t.Fatal(err)
	}
	err := MigrateLegacyCache(func(string, string) (bool, error) { return true, nil })
	if err == nil || !strings.Contains(err.Error(), "must not be hard linked") {
		t.Fatalf("MigrateLegacyCache() error = %v", err)
	}
}

func TestSynthesisCacheSupportsLongUnicodeHome(t *testing.T) {
	home := t.TempDir()
	for index := 0; len(utf16.Encode([]rune(home))) <= 300; index++ {
		home = filepath.Join(home, fmt.Sprintf("摘要-%02d-%s", index, strings.Repeat("长", 24)))
	}
	t.Setenv("COSLASH_HOME", home)
	if err := NewCache().Store("codex", "session", Record{Revision: 42}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCache().Load("codex", "session"); err != nil {
		t.Fatal(err)
	}
	windowstest.AssertPrivateACL(t, home, true)
	windowstest.AssertPrivateACL(t, SummariesDir(), true)
	windowstest.AssertPrivateACL(t, filepath.Join(SummariesDir(), "codex"), true)
	windowstest.AssertPrivateACL(t, filepath.Join(SummariesDir(), "codex", "session.json"), false)
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
	if err := NewCache().Store("codex", "session", Record{Revision: 42}); err == nil || !strings.Contains(err.Error(), "must not be a reparse point") {
		t.Fatalf("reparse home error = %v", err)
	}
}
