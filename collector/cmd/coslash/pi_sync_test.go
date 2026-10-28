package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

func TestPiStartupSharesPlannedInputsWithInventoryAndDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, "coslash"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", filepath.Join(home, "custom-sessions"))
	t.Setenv("OPENCODE_DB", filepath.Join(home, "missing.db"))
	t.Setenv("GROK_HOME", filepath.Join(home, "grok"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Chdir(home)
	write := func(name, id string) {
		t.Helper()
		path := filepath.Join(home, "custom-sessions", name+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"type":"session","version":3,"id":"`+id+`","timestamp":"2026-10-01T12:00:00Z"}`+"\n"+`{"type":"message","id":"u","timestamp":"2026-10-01T12:00:01Z","message":{"role":"user","content":"synthetic startup"}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("unrelated-name", "pi-startup")
	sources, err := localPiSourcePlan(t.Context())
	if err != nil || sources == nil || len(sources.Sources) != 1 {
		t.Fatalf("startup plan = %+v, %v", sources, err)
	}
	ctx := pi.WithSourcePlan(t.Context(), sources)
	write("later", "outside-snapshot")
	shared, err := localPiSourcePlan(ctx)
	if err != nil || shared != sources {
		t.Fatal("startup replaced the caller's source snapshot", err)
	}
	fingerprints, err := syncv4.OpenFingerprints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wake := make(chan struct{}, 1)
	runInventoryWithSchedule(ctx, fingerprints, queue, wake, &inventory.Tracker{}, func(context.Context) bool { return true }, func(context.Context, time.Duration, time.Time) (bool, bool, bool) {
		return false, false, false
	}, nil)
	if report := queue.Inventory(); report == nil || report.Files != 1 || len(report.Agents) != 1 || report.Agents[0].Agent != "pi" {
		t.Fatalf("planned inventory = %+v", report)
	}
	seen := 0
	if err := discoverLocalBatches(ctx, fingerprints, queue, func(batch syncv4.DiscoveryBatch) error {
		for _, item := range batch.Sessions {
			if item.Agent == "pi" {
				seen++
				if item.ID != "pi-startup" || len(batch.PiInputs) != 1 || batch.PiInputs[0].ID != item.ID || batch.PiInputs[0].Path != sources.Sources[0].Path {
					t.Fatalf("discovery lost planned inputs: %+v", batch)
				}
			}
		}
		return nil
	}); err != nil || seen != 1 {
		t.Fatalf("startup discovery count=%d, %v", seen, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := localPiSourcePlan(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("planning cancellation = %v", err)
	}
}
