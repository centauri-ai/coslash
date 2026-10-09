package hubclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileCredentialStoreRoundTripIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub-credentials", "hub.example.test")
	store := FileCredentialStore{Path: path}
	ctx := context.Background()

	if _, err := store.Load(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("Load before Save = %v, want ErrNotPaired", err)
	}
	if err := store.Save(ctx, "csl_dev_w_d_secret"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(ctx); err != nil || got != "csl_dev_w_d_secret" {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if err := store.Save(ctx, "csl_dev_w_d_rotated"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Load(ctx); got != "csl_dev_w_d_rotated" {
		t.Fatalf("Load after overwrite = %q", got)
	}
	if runtime.GOOS != "windows" {
		for name, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
			info, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Fatalf("%s mode = %o, want %o", name, got, want)
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("credential directory has %d entries, want only the credential", len(entries))
	}
}

func TestFileCredentialStoreRejectsEmptyAndDeletesConditionally(t *testing.T) {
	store := FileCredentialStore{Path: filepath.Join(t.TempDir(), "credential")}
	ctx := context.Background()
	if err := store.Save(ctx, "  "); err == nil {
		t.Fatal("Save accepted an empty credential")
	}
	if err := store.Save(ctx, "current"); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteIfMatches(ctx, "stale"); err != nil || deleted {
		t.Fatalf("DeleteIfMatches(stale) = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteIfMatches(ctx, "current"); err != nil || !deleted {
		t.Fatalf("DeleteIfMatches(current) = %v, %v", deleted, err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("Load after delete = %v", err)
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatalf("Delete of a missing credential = %v", err)
	}
}
