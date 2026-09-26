package directedhandoff

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

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
	target := &session.Session{Agent: "claude", ID: "target", SessionDetails: session.SessionDetails{FirstPrompt: &marker, Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestRecap, Description: answer}}}}
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
	target := &session.Session{Agent: "codex", ID: "resumed-target", SessionDetails: session.SessionDetails{FirstPrompt: &marker, Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestRecap, Description: "done"}}}}
	if err := store.Observe("local", []*session.Session{target}); err != nil {
		t.Fatal(err)
	}
	if got := store.List()[0]; got.Status != "completed" || got.TargetSessionID != "resumed-target" {
		t.Fatalf("resumed custom = %#v", got)
	}
}
