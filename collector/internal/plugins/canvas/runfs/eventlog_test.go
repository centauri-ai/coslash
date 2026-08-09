package runfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

func newTestEventLog(t *testing.T, options EventLogOptions) (*Scope, *EventLog, string) {
	t.Helper()
	scope, root := newTestScope(t, ScopeOptions{})
	log, err := NewEventLog(scope, "projects/example/runs/one/events.jsonl", options)
	if err != nil {
		t.Fatal(err)
	}
	return scope, log, root
}

func TestEventLogAppendReadAndIntentOrdering(t *testing.T) {
	clock := time.Date(2026, 8, 8, 12, 0, 0, 123, time.FixedZone("test", -7*60*60))
	_, log, root := newTestEventLog(t, EventLogOptions{Now: func() time.Time { return clock }})
	first, err := log.AppendIntent(context.Background(), "run_requested", map[string]string{"runId": "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := log.Append(context.Background(), "run_started", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Seq != 1 || second.Seq != 2 || !first.At.Equal(clock.UTC()) {
		t.Fatalf("unexpected envelopes: %#v %#v", first, second)
	}
	result, err := log.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 2 || result.TornTailBytes != 0 {
		t.Fatalf("result = %#v", result)
	}
	var payload map[string]string
	if err := json.Unmarshal(result.Events[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["runId"] != "one" {
		t.Fatalf("payload = %#v", payload)
	}
	info, err := os.Stat(filepath.Join(root, "projects", "example", "runs", "one", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("event log mode = %o", got)
	}
}

func TestEventLogConcurrentWritersAllocateGaplessSequences(t *testing.T) {
	baselineLocks := processLockCount()
	scope, first, _ := newTestEventLog(t, EventLogOptions{})
	second, err := NewEventLog(scope, first.name, EventLogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const writers = 64
	var group sync.WaitGroup
	errorsSeen := make(chan error, writers)
	for index := range writers {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			selected := first
			if index%2 == 1 {
				selected = second
			}
			_, err := selected.Append(context.Background(), "worker_finished", map[string]int{"worker": index})
			errorsSeen <- err
		}(index)
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := first.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != writers {
		t.Fatalf("event count = %d", len(result.Events))
	}
	workers := make([]int, 0, writers)
	for index, event := range result.Events {
		if event.Seq != uint64(index+1) {
			t.Fatalf("sequence[%d] = %d", index, event.Seq)
		}
		var data map[string]int
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		workers = append(workers, data["worker"])
	}
	sort.Ints(workers)
	for index, worker := range workers {
		if index != worker {
			t.Fatalf("workers = %v", workers)
		}
	}
	if got := processLockCount(); got != baselineLocks {
		t.Fatalf("process lock count = %d, want %d", got, baselineLocks)
	}
}

func TestEventLogDetectsAndRecoversTornTail(t *testing.T) {
	_, log, root := newTestEventLog(t, EventLogOptions{})
	if _, err := log.Append(context.Background(), "created", nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, filepath.FromSlash(log.name))
	if err := appendRaw(file, []byte(`{"seq":2,"at":"2026-08-08T12:00:00Z"`)); err != nil {
		t.Fatal(err)
	}
	before, err := log.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Events) != 1 || before.TornTailBytes == 0 {
		t.Fatalf("before = %#v", before)
	}
	if _, err := log.Append(context.Background(), "resumed", nil); err != nil {
		t.Fatal(err)
	}
	after, err := log.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Events) != 2 || after.Events[1].Seq != 2 || after.TornTailBytes != 0 {
		t.Fatalf("after = %#v", after)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || raw[len(raw)-1] != '\n' {
		t.Fatalf("repaired log is not newline terminated: %q", raw)
	}
}

func TestEventLogRecoverTruncatesOnlyTornTail(t *testing.T) {
	_, log, root := newTestEventLog(t, EventLogOptions{})
	if _, err := log.Append(context.Background(), "created", nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, filepath.FromSlash(log.name))
	if err := appendRaw(file, []byte(`{"complete":"json but no newline"}`)); err != nil {
		t.Fatal(err)
	}
	result, err := log.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TornTailBytes == 0 {
		t.Fatal("Recover did not report torn bytes")
	}
	recovered, err := log.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Events) != 1 || recovered.TornTailBytes != 0 {
		t.Fatalf("recovered = %#v", recovered)
	}
}

func TestEventLogRefusesCorruptDurableMiddleAndSequenceGap(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
	}{
		{name: "invalid_json", line: `{broken`},
		{name: "sequence_gap", line: `{"seq":5,"at":"2026-08-08T12:00:00Z","type":"gap"}`},
		{name: "blank_line", line: ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, log, root := newTestEventLog(t, EventLogOptions{})
			if _, err := log.Append(context.Background(), "created", nil); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, filepath.FromSlash(log.name))
			if err := appendRaw(file, []byte(test.line+"\n")); err != nil {
				t.Fatal(err)
			}
			if err := appendRaw(file, []byte(`{"seq":3,"at":"2026-08-08T12:00:00Z","type":"later"}`+"\n")); err != nil {
				t.Fatal(err)
			}
			_, err := log.Read(context.Background())
			var corruption *CorruptionError
			if !errors.As(err, &corruption) || corruption.Line != 2 {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestEventLogBoundsEventCountAndLog(t *testing.T) {
	_, log, _ := newTestEventLog(t, EventLogOptions{MaxEventBytes: 96, MaxLogBytes: 192, MaxEvents: 1})
	if _, err := log.Append(context.Background(), "created", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(context.Background(), "second", nil); !errors.Is(err, ErrLogFull) {
		t.Fatalf("event count error = %v", err)
	}

	_, largeLog, _ := newTestEventLog(t, EventLogOptions{MaxEventBytes: 128, MaxLogBytes: 256})
	if _, err := largeLog.Append(context.Background(), "large", map[string]string{"value": fmt.Sprintf("%0100d", 1)}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large event error = %v", err)
	}
}

func TestEventLogRejectsSymlinks(t *testing.T) {
	scope, root := newTestScope(t, ScopeOptions{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	parentLog, err := NewEventLog(scope, "linked/events.jsonl", EventLogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parentLog.Append(context.Background(), "event", nil); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked parent error = %v", err)
	}

	fileTarget := filepath.Join(outside, "events")
	if err := os.WriteFile(fileTarget, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fileTarget, filepath.Join(root, "events.jsonl")); err != nil {
		t.Fatal(err)
	}
	fileLog, err := NewEventLog(scope, "events.jsonl", EventLogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileLog.Append(context.Background(), "event", nil); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked file error = %v", err)
	}
}

func TestEventLogCancellationWhileWaitingForWriter(t *testing.T) {
	baseline := processLockCount()
	_, log, _ := newTestEventLog(t, EventLogOptions{})
	release, err := acquireProcessLock(context.Background(), log.lockKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := log.Append(ctx, "event", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	release()
	if got := processLockCount(); got != baseline {
		t.Fatalf("process lock count = %d, want %d", got, baseline)
	}
}

func TestEventLogReleasesProcessLocksForDistinctPaths(t *testing.T) {
	baseline := processLockCount()
	scope, _ := newTestScope(t, ScopeOptions{})
	for index := range 100 {
		log, err := NewEventLog(scope, fmt.Sprintf("runs/%d/events.jsonl", index), EventLogOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.Append(context.Background(), "created", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := processLockCount(); got != baseline {
		t.Fatalf("process lock count = %d, want %d", got, baseline)
	}
}

func TestEventLogRejectsZeroClockWithoutCorruptingLog(t *testing.T) {
	_, log, _ := newTestEventLog(t, EventLogOptions{Now: func() time.Time { return time.Time{} }})
	if _, err := log.Append(context.Background(), "created", nil); err == nil {
		t.Fatal("zero clock was accepted")
	}
	result, err := log.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 0 || result.TornTailBytes != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func appendRaw(file string, data []byte) error {
	opened, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer opened.Close()
	_, err = opened.Write(data)
	return err
}
