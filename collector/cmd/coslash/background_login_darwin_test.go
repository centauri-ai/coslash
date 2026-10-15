//go:build darwin

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackgroundLoginPlistEscapesTheExecutable(t *testing.T) {
	contents, err := backgroundLoginPlist(`/Applications/coSlash & <Local>`)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, `/Applications/coSlash &amp; &lt;Local&gt;`) || !strings.Contains(text, "--background") {
		t.Fatalf("launch agent plist does not safely encode its program: %s", text)
	}
}

func TestBackgroundLoginDoesNotReplaceAnotherLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, "dev"))
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// The fake reports an already loaded job, so bootstrap is never attempted.
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte("#!/bin/sh\n[ \"$1\" = print ] || exit 99\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	path := filepath.Join(home, "Library", "LaunchAgents", "io.coslash.local.plist")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("release login item")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := registerBackgroundLogin(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("startup replaced release login item: %s", got)
	}
	var output bytes.Buffer
	if code := runCLI(&output, &output, []string{"service", "install"}); code == 0 {
		t.Fatal("custom home installed a shared service")
	}
	t.Setenv("COSLASH_HOME", filepath.Join(home, ".coslash"))
	if err := registerBackgroundLogin(); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatal("default-home startup replaced release login item")
	}
	useFakeProtocolHandlerBackend(t, &fakeProtocolHandlerBackend{})
	t.Setenv(protocolHandlerTestHomeOverride, "1")
	if code := runCLI(&output, &output, []string{"service", "install"}); code != 0 {
		t.Fatalf("explicit install failed: %s", &output)
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Contains(got, []byte("--background")) {
		t.Fatalf("explicit install did not write login item: %s, %v", got, err)
	}
}
