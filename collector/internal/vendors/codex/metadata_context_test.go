package codex

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestReadSessionIndexRowsPreservesExactMatchAndRejectsDuplicate(t *testing.T) {
	home := t.TempDir()
	path := SessionIndexPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("{\"id\":\"wanted\",\"thread_name\":\"Fixture\"}\r\n")
	content := append([]byte("{\"id\":\"other\",\"thread_name\":\"Other\"}\n"), want...)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	rows, present, err := ReadSessionIndexRows(vendors.LocalReadSource, home, map[string]bool{"wanted": true})
	if err != nil || !present || len(rows) != 1 || !bytes.Equal(rows["wanted"], want) {
		t.Fatalf("rows = %#v, present = %t, error = %v", rows, present, err)
	}

	if err := os.WriteFile(path, append(want, want...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSessionIndexRows(vendors.LocalReadSource, home, map[string]bool{"wanted": true}); !errors.Is(err, vendors.ErrInvalidData) {
		t.Fatalf("duplicate error = %v; want invalid data", err)
	}

	for _, test := range []struct {
		name   string
		prefix []byte
	}{
		{name: "form feed", prefix: []byte{0x0c}},
		{name: "non-breaking space", prefix: []byte{0xc2, 0xa0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, append(test.prefix, want...), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := ReadSessionIndexRows(vendors.LocalReadSource, home, map[string]bool{"wanted": true}); !errors.Is(err, vendors.ErrInvalidData) {
				t.Fatalf("malformed whitespace error = %v; want invalid data", err)
			}
		})
	}
}

func TestLiveSessionIDsReadsRolloutPaths(t *testing.T) {
	const id = "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	output := "p123\nn/home/dev/.codex/sessions/rollout-2026-07-10T14-11-18-" + id + ".jsonl\ncodex\n"
	live := LiveSessionIDs(output)
	if _, ok := live[id]; !ok || len(live) != 1 {
		t.Fatalf("live = %#v", live)
	}
	if len(LiveSessionIDs("n/tmp/notes.txt\n")) != 0 {
		t.Fatal("non-rollout file was treated as live")
	}
}

func TestLoadLiveSessionsContextCancelsLsof(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "started")
	command := filepath.Join(directory, "lsof")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ntouch \"$LSOF_TEST_MARKER\"\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LSOF_TEST_MARKER", marker)

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := LoadLiveSessionsContext(ctx)
		finished <- err
	}()
	waitForMarker(t, marker)
	cancel()

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lsof process continued after cancellation")
	}
}

func waitForMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
