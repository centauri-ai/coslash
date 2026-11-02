//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeUpdateFixture(t *testing.T, target, backup string) {
	t.Helper()
	if err := os.WriteFile(target, []byte("1.2.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("1.2.3"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreHoldsBackupAgainstConcurrentDelete(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "coslash")
	backup := target + ".previous"
	read := func(path string) (string, error) { b, err := os.ReadFile(path); return string(b), err }
	writeUpdateFixture(t, target, backup)
	held, err := os.Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := settleLegacyTarget(target, "1.2.4", read); err == nil || errors.Is(err, errLegacyInstalledChanged) {
		held.Close()
		t.Fatalf("settlement deleted a backup that a rollback holds: %v", err)
	}
	held.Close()
	if got, err := os.ReadFile(backup); err != nil || string(got) != "1.2.3" {
		t.Fatalf("backup = %q, %v", got, err)
	}
	if err := restoreUpdateTarget(target, backup); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "1.2.3" {
		t.Fatalf("restored target = %q, %v", got, err)
	}
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup remains after restore: %v", err)
	}
}

func TestRestoreAfterSettlementKeepsInstalledTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "coslash")
	backup := target + ".previous"
	read := func(path string) (string, error) { b, err := os.ReadFile(path); return string(b), err }
	writeUpdateFixture(t, target, backup)
	if err := settleLegacyTarget(target, "1.2.4", read); err != nil {
		t.Fatal(err)
	}
	if err := restoreUpdateTarget(target, backup); err == nil {
		t.Fatal("rollback succeeded after settlement retired the backup")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "1.2.4" {
		t.Fatalf("installed target = %q, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("restore left files behind: %v %v", entries, err)
	}
}
