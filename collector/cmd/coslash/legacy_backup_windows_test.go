//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// legacyRestore is the restore code of the published v0.4.2 helper
// (8d68f64bd0330c93e820135c2994c1d5e1b39ea6, update_process_windows.go). It is
// copied here on purpose: the product restoreUpdateTarget may differ, but an
// already shipped helper cannot change.
func legacyRestore(target, backup string) error {
	from, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func readTestVersion(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func writeLegacyPair(t *testing.T, target, backup string) {
	t.Helper()
	if err := os.WriteFile(target, []byte("1.2.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("1.2.3"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// While settlement holds the backup, the legacy helper cannot roll back, and
// verification observes the installed version it will keep.
func TestLegacyRetirementExcludesLegacyRollbackWhileVerifying(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "coslash.exe")
	backup := target + ".previous"
	writeLegacyPair(t, target, backup)
	var restoreErr error
	verified := 0
	read := func(path string) (string, error) {
		verified++
		if verified == 2 { // the verification under the hold
			restoreErr = legacyRestore(target, backup)
		}
		return readTestVersion(path)
	}
	if err := settleLegacyTarget(target, "1.2.4", read); err != nil {
		t.Fatal(err)
	}
	if verified != 3 {
		t.Fatalf("expected check, check under hold, final check; got %d", verified)
	}
	if restoreErr == nil {
		t.Fatal("legacy rollback replaced the target while settlement held the backup")
	}
	if got, _ := os.ReadFile(target); string(got) != "1.2.4" {
		t.Fatalf("target changed to %q", got)
	}
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup not retired: %v", err)
	}
}

// A rollback that already holds the backup makes settlement fail without
// deleting it; retrying after the rollback reports the changed version.
func TestLegacyRetirementYieldsToRollbackInProgress(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "coslash.exe")
	backup := target + ".previous"
	writeLegacyPair(t, target, backup)
	held, err := os.Open(backup) // Go opens without FILE_SHARE_DELETE, like an in-flight rename
	if err != nil {
		t.Fatal(err)
	}
	if err := settleLegacyTarget(target, "1.2.4", readTestVersion); err == nil {
		t.Fatal("settlement deleted a backup that is in use")
	}
	_ = held.Close()
	if _, err := os.Lstat(backup); err != nil {
		t.Fatalf("backup lost: %v", err)
	}
	if err := legacyRestore(target, backup); err != nil {
		t.Fatal(err)
	}
	if err := settleLegacyTarget(target, "1.2.4", readTestVersion); !errors.Is(err, errLegacyInstalledChanged) {
		t.Fatalf("rollback not reported: %v", err)
	}
}

// One winner, with the published legacy restore as the rollback side.
func TestLegacySettlementAndLegacyHelperRollbackHaveOneWinner(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "coslash.exe")
	backup := target + ".previous"
	for i := 0; i < 200; i++ {
		writeLegacyPair(t, target, backup)
		start := make(chan struct{})
		settled, restored := make(chan error, 1), make(chan error, 1)
		go func() { <-start; settled <- settleLegacyTarget(target, "1.2.4", readTestVersion) }()
		go func() { <-start; restored <- legacyRestore(target, backup) }()
		close(start)
		settleErr, restoreErr := <-settled, <-restored
		disk, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("iteration %d: target lost (settle=%v restore=%v): %v", i, settleErr, restoreErr, err)
		}
		if settleErr == nil && (string(disk) != "1.2.4" || restoreErr == nil) {
			t.Fatalf("iteration %d: settlement reported after rollback: %q %v", i, disk, restoreErr)
		}
		if restoreErr == nil && (string(disk) != "1.2.3" || settleErr == nil) {
			t.Fatalf("iteration %d: rollback did not prevent settlement: %q %v", i, disk, settleErr)
		}
		_ = os.Remove(backup)
	}
}
