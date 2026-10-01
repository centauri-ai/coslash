//go:build !windows

package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRepairDiscoveryRootCompatibility(t *testing.T) {
	home, path, db, _ := deleteFixture(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	effective := filepath.Join(filepath.Dir(path), "opencode-dev.db")
	if err := os.Rename(path, effective); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fake := filepath.Join(bin, "opencode")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' '"+effective+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	discovered, err := RootContext(context.Background())
	if err != nil || discovered != effective {
		t.Fatalf("discovery %q %v", discovered, err)
	}
	runSessionDelete = deleteDatabaseSession
	if err := DeleteSession(context.Background(), home, "ses_target"); err != nil {
		t.Fatalf("discovered supported DB cannot be deleted: %v", err)
	}
}
