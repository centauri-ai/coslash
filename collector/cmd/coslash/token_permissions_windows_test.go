package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/centauri-ai/coslash/collector/internal/windowstest"
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
	windowstest.AssertPrivateACL(t, home, true)
	windowstest.AssertPrivateACL(t, path, false)
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
	windowstest.AssertPrivateACL(t, path, directory)
}
