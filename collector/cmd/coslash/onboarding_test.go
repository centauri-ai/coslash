package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoredHubURLContainsOnlyAValidatedHubOrigin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := writeStoredHubURL("https://beta.coslash.io"); err != nil {
		t.Fatal(err)
	}
	got, err := readStoredHubURL()
	if err != nil || got != "https://beta.coslash.io" {
		t.Fatalf("stored Hub URL=%q error=%v", got, err)
	}
	info, err := os.Stat(filepath.Join(home, storedHubURLFilename))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("Hub address file permissions=%#o", info.Mode().Perm())
	}
	for _, invalid := range []string{"https://beta.coslash.io/path", "https://evil.example", "http://evil.example"} {
		if err := writeStoredHubURL(invalid); err == nil {
			t.Fatalf("writeStoredHubURL(%q) succeeded", invalid)
		}
	}
}
