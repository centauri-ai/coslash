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

func TestLoadLiveSessionsCapsCandidateRollouts(t *testing.T) {
	files := make([]string, maxWindowsLiveRolloutCandidates+10)
	for index := range files {
		files[index] = fmt.Sprintf(`C:\rollout-%04d.jsonl`, index)
	}
	if got := newestRolloutCandidates(files, maxWindowsLiveRolloutCandidates); len(got) != maxWindowsLiveRolloutCandidates {
		t.Fatalf("candidate count = %d", len(got))
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
