package main

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

func TestRunInventoryRecordsForCheckInAndWakesTheSyncLoopOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	fingerprints, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInventory(ctx, fingerprints, queue, wake, func(context.Context) bool { return true })
	}()
	select {
	case <-wake:
	case <-time.After(10 * time.Second):
		t.Fatal("inventory did not wake the sync loop")
	}
	if inventory := queue.Inventory(); inventory == nil || inventory.Files != 0 || inventory.ScannedAt == "" {
		t.Fatalf("queue inventory = %+v", inventory)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory loop did not stop on cancellation")
	}
}

func TestRunInventoryWaitsForPairing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	fingerprints, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var paired atomic.Bool
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInventory(ctx, fingerprints, queue, wake, func(context.Context) bool { return paired.Load() })
	}()
	select {
	case <-wake:
		t.Fatal("inventory woke before pairing")
	case <-time.After(100 * time.Millisecond):
	}
	if queue.Inventory() != nil {
		t.Fatal("inventory scanned before pairing")
	}
	paired.Store(true)
	select {
	case <-wake:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory did not wake after pairing")
	}
	cancel()
	<-done
}
