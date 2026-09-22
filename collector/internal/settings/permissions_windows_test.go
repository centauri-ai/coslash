package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/centauri-ai/coslash/collector/internal/windowstest"
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
	windowstest.AssertPrivateACL(t, Home(), true)
	windowstest.AssertPrivateACL(t, Path(), false)
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
	windowstest.AssertPrivateACL(t, Home(), true)
	windowstest.AssertPrivateACL(t, Path(), false)
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
	windowstest.SetDACL(t, home, "D:(A;OICI;FA;;;WD)", true)
	windowstest.SetDACL(t, Path(), "D:(A;;FA;;;WD)", false)

	state := Open().State()
	if !state.Valid || !state.Persisted || state.Error != "" {
		t.Fatalf("migrated settings = %+v", state)
	}
	windowstest.AssertPrivateACL(t, home, true)
	windowstest.AssertPrivateACL(t, Path(), false)
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
