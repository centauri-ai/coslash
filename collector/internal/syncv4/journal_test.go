package syncv4

import (
	"fmt"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestCommandJournalPrunesAgeAndCount(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue := &Queue{}
	queue.state.Commands = append(queue.state.Commands,
		commandRecord{ID: "expired", At: now.Add(-8 * 24 * time.Hour)},
		commandRecord{ID: "legacy"})
	for i := range 1001 {
		queue.state.Commands = append(queue.state.Commands, commandRecord{ID: fmt.Sprint(i), At: now.Add(-time.Hour)})
	}
	queue.pruneCommands(now)
	if len(queue.state.Commands) != commandJournalLimit {
		t.Fatalf("journal size = %d", len(queue.state.Commands))
	}
	if queue.state.Commands[0].ID != "1" || queue.state.Commands[len(queue.state.Commands)-1].ID != "1000" {
		t.Fatalf("journal bounds = %q ... %q", queue.state.Commands[0].ID, queue.state.Commands[len(queue.state.Commands)-1].ID)
	}
	queue.state.Commands = []commandRecord{{ID: "legacy"}}
	queue.pruneCommands(now)
	if !queue.state.Commands[0].At.Equal(now) {
		t.Fatal("legacy command lost its retention timestamp")
	}
}

func TestCommandJournalKeepsUnacknowledgedResults(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue := &Queue{}
	queue.state.Commands = append(queue.state.Commands, commandRecord{
		ID: "old-pending", At: now.Add(-8 * 24 * time.Hour),
		Result: hubclient.V4CommandResult{CommandID: "old-pending", Result: "in_progress"},
	})
	for i := range commandJournalLimit + 10 {
		queue.state.Commands = append(queue.state.Commands, commandRecord{ID: fmt.Sprint(i), At: now})
	}
	queue.pruneCommands(now)
	if len(queue.state.Commands) != commandJournalLimit || queue.state.Commands[0].ID != "old-pending" {
		t.Fatalf("pending result was pruned: %d records, first %q", len(queue.state.Commands), queue.state.Commands[0].ID)
	}
}

func TestCommandProgressStaysOpenUntilTerminalResult(t *testing.T) {
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	const id = "cmd_retry"
	if started, err := queue.StartCommand(id); err != nil || !started {
		t.Fatalf("start = %v, %v", started, err)
	}
	queue.HoldCommand(id)
	if err := queue.SetCommandProgress(id, hubclient.V4CommandProgress{Stage: "uploading", BytesDone: 4, BytesTotal: 8}); err != nil {
		t.Fatal(err)
	}
	results := queue.Results()
	if len(results) != 1 || results[0].Result != "in_progress" || results[0].Progress.Stage != "uploading" {
		t.Fatalf("in-flight result = %+v", results)
	}
	if err := queue.AcknowledgeResults(results); err != nil {
		t.Fatal(err)
	}
	if len(queue.Results()) != 1 {
		t.Fatal("Hub acknowledgement closed an in-flight command")
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if result := reopened.Results(); len(result) != 1 || result[0].Result != "failed" || result[0].Error != "execution_interrupted" {
		t.Fatalf("restarted command = %+v", result)
	}
	if err := queue.FinishCommand(hubclient.V4CommandResult{CommandID: id, Result: "done", Progress: &hubclient.V4CommandProgress{Stage: "landed", BytesDone: 8, BytesTotal: 8}}); err != nil {
		t.Fatal(err)
	}
	results = queue.Results()
	if len(results) != 1 || results[0].Result != "done" || results[0].Progress.Stage != "landed" {
		t.Fatalf("terminal result = %+v", results)
	}
}

func TestCommandResultsPrioritizeTerminalOutcomes(t *testing.T) {
	queue := &Queue{}
	for i := range 100 {
		id := fmt.Sprint(i)
		queue.state.Commands = append(queue.state.Commands, commandRecord{ID: id,
			Result: hubclient.V4CommandResult{CommandID: id, Result: "in_progress", Progress: &hubclient.V4CommandProgress{Stage: "uploading"}}})
	}
	queue.state.Commands = append(queue.state.Commands, commandRecord{ID: "terminal",
		Result: hubclient.V4CommandResult{CommandID: "terminal", Result: "failed", Error: "retry_failed"}})
	results := queue.Results()
	if len(results) != 100 || results[0].CommandID != "terminal" {
		t.Fatalf("terminal result starved by progress: %+v", results[:min(len(results), 2)])
	}
}
