package directedhandoff

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

func TestShutdownCancelsReviewsAndRefusesNewWork(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "handoffs.json"))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	if !store.RunReview(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}) {
		t.Fatal("review did not start")
	}
	<-started
	store.Shutdown()
	<-finished
	if store.RunReview(func(context.Context) { t.Error("started after shutdown") }) {
		t.Fatal("review accepted after shutdown")
	}
}

func TestStorePersistsAndCorrelatesExactMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoffs.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Start("local", "codex", "origin", "claude", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "running" || record.Activity != "starting" {
		t.Fatalf("start = %#v", record)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.List(); len(got) != 1 || got[0].ID != record.ID {
		t.Fatalf("restored = %#v", got)
	}
	marker := Marker(record.ID)
	for _, prompt := range []string{"prefix " + marker, marker + " suffix", "coSlash handoff ID: " + record.ID + "x"} {
		if store.Observe("local", []*session.Session{{Agent: "claude", ID: "wrong", SessionDetails: session.SessionDetails{FirstPrompt: &prompt}}}) != nil {
			t.Fatal("accepted partial marker")
		}
	}
	answer := "Done with the request"
	target := &session.Session{Agent: "claude", ID: "target", SessionDetails: session.SessionDetails{FirstPrompt: &marker, Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestRecap, Description: answer + "\ncoSlash handoff completed: " + record.ID}}}}
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	got := store.List()[0]
	if got.TargetSessionID != "target" || got.Status != "completed" || got.Result != answer {
		t.Fatalf("completed = %#v", got)
	}
}

func TestDiscoveryWarningRecoversAndOnlyConfirmedStopFails(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "handoffs.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	store.now = func() time.Time { return now }
	record, err := store.Start("local", "codex", "origin", "claude", "custom")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	if err := store.Observe("local", nil); err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Status != "running" || got.Activity != "not_detected" {
		t.Fatalf("warning = %#v", got)
	}
	marker := Marker(record.ID)
	target := &session.Session{Agent: "claude", ID: "target", SessionDetails: session.SessionDetails{FirstPrompt: &marker}}
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Status != "running" || got.Activity != "working" {
		t.Fatalf("recovered = %#v", got)
	}
	inactive := "inactive"
	target.Status = &inactive
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Status != "failed" {
		t.Fatalf("stopped = %#v", got)
	}
}

func TestHeadlessReviewDoesNotEnterSessionDiscovery(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "handoffs.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	store.now = func() time.Time { return now }
	record, err := store.Start("local", "codex", "origin", "codex", "review")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	marker := Marker(record.ID)
	target := &session.Session{Agent: "codex", ID: "unrelated", SessionDetails: session.SessionDetails{FirstPrompt: &marker}}
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Activity != "starting" || got.TargetSessionID != "" {
		t.Fatalf("review discovery = %#v", got)
	}
}

func TestNewerHandoffDoesNotReplaceOlderTracking(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "handoffs.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Start("local", "codex", "origin", "claude", "review")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Start("local", "codex", "origin", "codex", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.List(); len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("list = %#v", got)
	}
	if err := store.Complete(first.ID, "reviewed"); err != nil {
		t.Fatal(err)
	}
	if got := store.List(); got[0].Status != "running" || got[1].Status != "completed" {
		t.Fatalf("statuses = %#v", got)
	}
	marker := Marker(second.ID)
	idle := "idle"
	target := &session.Session{Agent: "codex", ID: "new-target", Status: &idle, SessionDetails: session.SessionDetails{FirstPrompt: &marker, Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestQuestion, Description: "Need clarification"}, {Turn: 2, Category: session.DigestRecap, Description: "Later answer"}}}}
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	if got := store.List(); got[0].Status != "running" || got[0].Activity != "needs_input" || got[1].TargetSessionID != "" {
		t.Fatalf("question/concurrent = %#v", got)
	}
	waiting := "waiting"
	target.Status = &waiting
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Activity != "needs_input" {
		t.Fatalf("waiting target = %#v", got)
	}
}

func TestRecoverReviewsPreservesCustomDiscovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoffs.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	review, err := store.Start("local", "codex", "origin", "claude", "review")
	if err != nil {
		t.Fatal(err)
	}
	custom, err := store.Start("local", "codex", "origin", "codex", "custom")
	if err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverReviews(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]Record{}
	for _, record := range store.List() {
		states[record.ID] = record
	}
	if states[review.ID].Status != "failed" || states[review.ID].Error == "" {
		t.Fatalf("review after restart = %#v", states[review.ID])
	}
	if states[custom.ID].Status != "running" {
		t.Fatalf("custom after restart = %#v", states[custom.ID])
	}
	marker := Marker(custom.ID)
	target := &session.Session{Agent: "codex", ID: "resumed-target", SessionDetails: session.SessionDetails{FirstPrompt: &marker, Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestRecap, Description: "done\ncoSlash handoff completed: " + custom.ID}}}}
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Status != "completed" || got.TargetSessionID != "resumed-target" {
		t.Fatalf("resumed custom = %#v", got)
	}
}

func TestCustomHandoffWaitsForClarificationAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoffs.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Start("local", "codex", "origin", "claude", "custom")
	if err != nil {
		t.Fatal(err)
	}
	marker, idle := Marker(record.ID), "idle"
	target := &session.Session{Agent: "claude", ID: "target", Status: &idle, SessionDetails: session.SessionDetails{
		FirstPrompt: &marker,
		Digest:      []session.DigestEntry{{Turn: 1, Category: session.DigestRecap, Description: "Which fixture color do you want me to use? I will wait for your answer before I do anything else."}},
	}}
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Status != "running" || got.Activity != "needs_input" || got.Result != "" || got.TargetSessionID != "target" {
		t.Fatalf("unanswered clarification after restart = %#v", got)
	}
	target.Digest = append(target.Digest,
		session.DigestEntry{Turn: 2, Category: session.DigestUser, Description: "Blue"},
		session.DigestEntry{Turn: 2, Category: session.DigestRecap, Description: "Used blue.\ncoSlash handoff completed: " + record.ID},
	)
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Status != "completed" || got.Result != "Used blue." || got.Activity != "" {
		t.Fatalf("completed after clarification = %#v", got)
	}
}

func TestCustomHandoffRequiresItsOwnAssistantCompletionMarker(t *testing.T) {
	for _, test := range []struct {
		name, category, reply, status, result string
	}{
		{"ordinary recap", session.DigestRecap, "Done.", "running", ""},
		{"wrong marker", session.DigestRecap, "Done.\ncoSlash handoff completed: wrong", "running", ""},
		{"user marker", session.DigestUser, "Done.\ncoSlash handoff completed: HANDOFF_ID", "running", ""},
		{"quoted marker", session.DigestRecap, "Example:\n> coSlash handoff completed: HANDOFF_ID", "running", ""},
		{"marker without result", session.DigestRecap, "coSlash handoff completed: HANDOFF_ID", "running", ""},
		{"marker in middle", session.DigestRecap, "Example:\ncoSlash handoff completed: HANDOFF_ID\nWhich color?", "running", ""},
		{"leading completion", session.DigestRecap, "coSlash handoff completed: HANDOFF_ID\nDone.", "completed", "Done."},
		{"leading wrong marker", session.DigestRecap, "coSlash handoff completed: wrong\nDone.", "running", ""},
		{"leading user marker", session.DigestUser, "coSlash handoff completed: HANDOFF_ID\nDone.", "running", ""},
		{"leading quoted marker", session.DigestRecap, "> coSlash handoff completed: HANDOFF_ID\nDone.", "running", ""},
		{"direct completion", session.DigestRecap, "Done.\ncoSlash handoff completed: HANDOFF_ID", "completed", "Done."},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "handoffs.json"))
			if err != nil {
				t.Fatal(err)
			}
			record, err := store.Start("ssh:host", "claude", "origin", "codex", "custom")
			if err != nil {
				t.Fatal(err)
			}
			marker := Marker(record.ID)
			target := &session.Session{Agent: "codex", ID: "target", SessionDetails: session.SessionDetails{
				FirstPrompt: &marker,
				Digest:      []session.DigestEntry{{Turn: 1, Category: test.category, Description: strings.ReplaceAll(test.reply, "HANDOFF_ID", record.ID)}},
			}}
			if err := store.Observe("ssh:host", []*session.Session{target}); err != nil {
				t.Fatal(err)
			}
			if got := store.List()[0]; got.Status != test.status || got.Result != test.result {
				t.Fatalf("observed = %#v", got)
			}
		})
	}
}

func TestCustomHandoffCompletesAfterTranscriptRecapTruncation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "handoffs.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Start("local", "codex", "origin", "claude", "custom")
	if err != nil {
		t.Fatal(err)
	}
	reply := CompletionMarker(record.ID) + "\n" + strings.Repeat("x", fullsessionv1.MaxStringBytes+1)
	path := filepath.Join(t.TempDir(), "target.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	encoder := json.NewEncoder(file)
	for _, row := range []any{
		map[string]any{"type": "user", "message": map[string]any{"content": Marker(record.ID)}},
		map[string]any{"type": "assistant", "message": map[string]any{"stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": reply}}}},
	} {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	parsed, failures, err := claude.ParseRemoteFiles(vendors.LocalReadSource, []string{path})
	if err != nil || len(failures) != 0 || len(parsed) != 1 {
		t.Fatalf("parse transcript: err=%v, failures=%v, sessions=%d", err, failures, len(parsed))
	}
	if err := store.Observe("local", []*session.Session{parsed[0].Session}); err != nil {
		t.Fatal(err)
	}
	got := store.List()[0]
	if got.Status != "completed" || got.TargetSessionID != "target" || got.Result == "" || len(got.Result) > fullsessionv1.MaxStringBytes || strings.Contains(got.Result, CompletionMarker(record.ID)) {
		t.Fatalf("truncated recap: status=%s target=%s resultBytes=%d", got.Status, got.TargetSessionID, len(got.Result))
	}
}
