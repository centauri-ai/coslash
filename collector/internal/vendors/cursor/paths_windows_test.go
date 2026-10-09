//go:build windows

package cursor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorGlobalStorageUsesRedirectedAppData(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	roaming := t.TempDir()
	t.Setenv("APPDATA", roaming)
	want := filepath.Join(roaming, "Cursor", "User", "globalStorage")
	if got := GlobalStorage(home); got != want {
		t.Fatalf("GlobalStorage() = %q, want %q", got, want)
	}
}
