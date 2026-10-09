//go:build !windows

package cursor

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestCursorGlobalStorageUsesPlatformLayout(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "dev")
	t.Setenv("XDG_CONFIG_HOME", "")
	want := filepath.Join(home, ".config", "Cursor", "User", "globalStorage")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage")
	}
	if got := GlobalStorage(home); got != want {
		t.Fatalf("cursorGlobalStorage = %q, want %q", got, want)
	}
	if runtime.GOOS == "darwin" {
		return
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(string(filepath.Separator), "xdg"))
	if got, want := GlobalStorage(home), filepath.Join(string(filepath.Separator), "xdg", "Cursor", "User", "globalStorage"); got != want {
		t.Fatalf("cursorGlobalStorage with XDG_CONFIG_HOME = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if got, want := GlobalStorage(home), filepath.Join(home, ".config", "Cursor", "User", "globalStorage"); got != want {
		t.Fatalf("cursorGlobalStorage ignores a relative XDG_CONFIG_HOME: got %q", got)
	}
}
