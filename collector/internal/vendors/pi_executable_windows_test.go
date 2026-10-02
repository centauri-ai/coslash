package vendors

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPiExecutableFindsManagedWindowsInstallAndPrefersPATH(t *testing.T) {
	home := t.TempDir()
	pathBin := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", pathBin)
	managed := filepath.Join(home, ".pi", "agent", "bin", "pi.cmd")
	if err := os.MkdirAll(filepath.Dir(managed), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("@echo off\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := PiExecutable(); err != nil || got != managed {
		t.Fatalf("managed executable = %q, %v; want %q", got, err, managed)
	}
	onPATH := filepath.Join(pathBin, "pi.cmd")
	if err := os.WriteFile(onPATH, []byte("@echo off\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := PiExecutable(); err != nil || got != onPATH {
		t.Fatalf("PATH executable = %q, %v; want %q", got, err, onPATH)
	}
	if err := os.Remove(onPATH); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	if got, err := PiExecutable(); err == nil || got != "" {
		t.Fatalf("missing executable = %q, %v; want an error", got, err)
	}
}
