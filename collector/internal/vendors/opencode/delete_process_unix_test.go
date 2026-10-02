//go:build !windows

package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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

func TestDeletionJSONFIFORefusesWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &deletionReadBoundaryContext{Context: parent, at: 1, action: cancel}
	finished := make(chan error, 1)
	go func() {
		var journal deletionJournal
		_, err := readDeletionJSON(ctx, path, 16<<20, &journal)
		finished <- err
	}()
	<-parent.Done()
	for {
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrSessionUnverified) {
				t.Fatalf("FIFO refusal: %v", err)
			}
			return
		default:
		}
		writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			writer.Close()
			<-finished
			t.Fatal("receipt open blocked until a FIFO writer released it")
		}
		if !errors.Is(err, syscall.ENXIO) {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
}

func TestDeletionJSONReplacementSpecialFile(t *testing.T) {
	for _, mode := range []string{"FIFO", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipt.json")
			if err := os.WriteFile(path, []byte(`{"Version":1}`), 0600); err != nil {
				t.Fatal(err)
			}
			ctx := &deletionReadBoundaryContext{Context: context.Background(), at: 2, action: func() {
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				var err error
				if mode == "FIFO" {
					err = syscall.Mkfifo(path, 0600)
				} else {
					err = os.Symlink(path+".original", path)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			var journal deletionJournal
			if found, err := readDeletionJSON(ctx, path, 16<<20, &journal); err == nil || found {
				t.Fatal("replacement special file accepted")
			}
		})
	}
}
