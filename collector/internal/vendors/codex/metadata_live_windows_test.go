//go:build windows

package codex

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func stubWindowsLiveness(t *testing.T, query func([]string) ([]uint32, error), identify func(uint32) bool) {
	t.Helper()
	originalQuery, originalIdentify := processesUsingRollouts, codexProcess
	t.Cleanup(func() { processesUsingRollouts, codexProcess = originalQuery, originalIdentify })
	processesUsingRollouts, codexProcess = query, identify
}

func TestLoadLiveSessionsRequiresCodexProcess(t *testing.T) {
	id := "01a0b1a9-d254-7162-af80-9e5d3a5afc3e"
	path := filepath.Join(t.TempDir(), "rollout-2026-09-17T10-00-00-"+id+".jsonl")
	stubWindowsLiveness(t, func(paths []string) ([]uint32, error) {
		if !slices.Equal(paths, []string{path}) {
			t.Fatalf("paths = %v", paths)
		}
		return []uint32{42}, nil
	}, func(pid uint32) bool { return pid == 42 })

	live, err := loadLiveSessionsFromFiles(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := live[id]; !ok {
		t.Fatalf("live sessions = %v, want %q", live, id)
	}
}

func TestLoadLiveSessionsIgnoresUnrelatedFileHolder(t *testing.T) {
	stubWindowsLiveness(t, func([]string) ([]uint32, error) { return []uint32{7}, nil }, func(uint32) bool { return false })
	live, err := loadLiveSessionsFromFiles(context.Background(), []string{`C:\rollout-01a0b1a9-d254-7162-af80-9e5d3a5afc3e.jsonl`})
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("live sessions = %v, want none", live)
	}
}

func TestLoadLiveSessionsBatchesInactiveRollouts(t *testing.T) {
	files := make([]string, 8)
	for index := range files {
		files[index] = fmt.Sprintf(`C:\rollout-00000000-0000-0000-0000-%012d.jsonl`, index)
	}
	livePath := files[len(files)-1]
	queries := 0
	stubWindowsLiveness(t, func(paths []string) ([]uint32, error) {
		queries++
		if slices.Contains(paths, livePath) {
			return []uint32{42}, nil
		}
		return nil, nil
	}, func(pid uint32) bool { return pid == 42 })

	live, err := loadLiveSessionsFromFiles(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || queries >= len(files) {
		t.Fatalf("live=%v queries=%d, want one live session with fewer than %d queries", live, queries, len(files))
	}
}

func TestLoadLiveSessionsQueriesEveryBoundedChunk(t *testing.T) {
	files := make([]string, maxFilesPerRestartManagerQuery*2+10)
	for index := range files {
		files[index] = fmt.Sprintf(`C:\rollout-00000000-0000-0000-0000-%012d.jsonl`, index)
	}
	want := files[0]
	queried := map[string]bool{}
	stubWindowsLiveness(t, func(paths []string) ([]uint32, error) {
		if len(paths) > maxFilesPerRestartManagerQuery {
			t.Fatalf("query contains %d paths", len(paths))
		}
		for _, path := range paths {
			queried[path] = true
		}
		if slices.Contains(paths, want) {
			return []uint32{42}, nil
		}
		return nil, nil
	}, func(pid uint32) bool { return pid == 42 })

	live, err := loadLiveSessionsFromFiles(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if len(queried) != len(files) {
		t.Fatalf("queried %d of %d rollout paths", len(queried), len(files))
	}
	if _, ok := live[SessionIDFromRollout(want)]; !ok {
		t.Fatalf("live sessions = %v, want oldest rollout", live)
	}
}

func TestLoadLiveSessionsPreservesRestartManagerError(t *testing.T) {
	want := errors.New("restart manager unavailable")
	stubWindowsLiveness(t, func([]string) ([]uint32, error) { return nil, want }, func(uint32) bool { return true })
	_, err := loadLiveSessionsFromFiles(context.Background(), []string{`C:\rollout.jsonl`})
	if !errors.Is(err, want) {
		t.Fatalf("loadLiveSessionsFromFiles() = %v, want %v", err, want)
	}
}

func TestLoadLiveSessionsHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stubWindowsLiveness(t, func([]string) ([]uint32, error) {
		t.Fatal("queried files after cancellation")
		return nil, nil
	}, func(uint32) bool { return true })

	_, err := loadLiveSessionsFromFiles(ctx, []string{`C:\rollout.jsonl`})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadLiveSessionsFromFiles() = %v, want %v", err, context.Canceled)
	}
}
