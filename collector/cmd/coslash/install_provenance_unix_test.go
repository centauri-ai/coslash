//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedCustomScriptInstall(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "custom install")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "coslash")
	marker := filepath.Join(dir, scriptInstallMarker)
	if err := os.WriteFile(executable, []byte("release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if trustedCustomScriptInstall(executable, home) {
		t.Fatal("accepted an unregistered executable")
	}
	if err := os.WriteFile(marker, []byte(scriptInstallMarkerContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if !trustedCustomScriptInstall(executable, home) {
		t.Fatal("rejected the installer-owned path")
	}
	if trustedCustomScriptInstall(executable, filepath.Join(home, "elsewhere")) {
		t.Fatal("accepted an executable outside the owner's home")
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if trustedCustomScriptInstall(executable, home) {
		t.Fatal("accepted a group- or world-writable directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(marker, 0o666); err != nil {
		t.Fatal(err)
	}
	if trustedCustomScriptInstall(executable, home) {
		t.Fatal("accepted a group- or world-writable marker")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, marker); err != nil {
		t.Fatal(err)
	}
	if trustedCustomScriptInstall(executable, home) {
		t.Fatal("accepted a symlink marker")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("another installer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if trustedCustomScriptInstall(executable, home) {
		t.Fatal("accepted an unrelated marker")
	}
	if err := os.WriteFile(marker, []byte(scriptInstallMarkerContents), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if trustedCustomScriptInstall(filepath.Join(link, "coslash"), home) {
		t.Fatal("accepted a symlink parent")
	}
}
