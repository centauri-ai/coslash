package syncv4

import (
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
)

func TestLocationRefreshNegotiatesAndPreservesSyncedContent(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	if err := session.UseLocationMemory(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.UseLocationMemory("") })
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := hubclient.V4Config{ImportPlan: &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}}
	apply := func(caps []string) {
		t.Helper()
		if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: cfg, Capabilities: caps}, now); err != nil {
			t.Fatal(err)
		}
	}
	apply(nil)
	s := &session.Session{ID: "local-id", Agent: "codex", WorkingDirectory: "/repo/work", LastActivityTime: 1}
	entry := Entry{Key: "key", Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: "key"}, Selection: sessionbackupproducer.Selection{SourceID: "local", Agent: "codex", SessionID: s.ID}, Activity: 1, SyncedActivity: 1, SourceRevision: "unchanged", SyncedSourceRevision: "unchanged", SessionID: "ses_key", RevisionID: "revision", Listed: true}
	if err = q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: cfg.ImportPlan}
	r := Runner{Queue: q, Hub: hub, config: cfg, checkedAt: now, Now: func() time.Time { return now }}
	probe := &session.CheckoutObservation{Location: session.Location{Root: s.WorkingDirectory, Label: "work", Repo: "example.org/o/project", Main: false, Availability: "available", HeadKind: "branch", Head: "feature", LastSeen: now.UnixMilli()}, CloneKey: "clone", CheckoutKey: "tree"}
	observe := func() {
		t.Helper()
		if err := session.RecordLocations(ctx, "local", []*session.Session{s}, map[string]*session.CheckoutObservation{s.WorkingDirectory: probe}); err != nil {
			t.Fatal(err)
		}
	}
	refresh := func() {
		t.Helper()
		if err := r.syncLocations(ctx); err != nil {
			t.Fatal(err)
		}
	}
	observe()
	refresh()
	if len(hub.lists) != 0 {
		t.Fatal("sent location to a legacy Hub")
	}
	apply([]string{hubclient.CapabilityWorktreeLabels})
	refresh()
	if len(hub.lists) != 1 || len(hub.lists[0]) != 1 {
		t.Fatalf("listing batches: %+v", hub.lists)
	}
	first := hub.lists[0][0]
	if first.Worktree == nil || first.Worktree.Label != "work" || first.Location.Root != "/repo/work" || first.Repo != "example.org/o/project" || first.LocationVersion == 0 || first.Branch != "" {
		t.Fatalf("wrong location listing %+v", first)
	}
	refresh()
	if len(hub.lists) != 1 {
		t.Fatal("unchanged projection was sent again")
	}
	probe.Head = "next"
	observe()
	refresh()
	if len(hub.lists) != 2 || hub.lists[1][0].LocationVersion <= first.LocationVersion || hub.lists[1][0].Worktree.ID != first.Worktree.ID {
		t.Fatalf("branch change: %+v", hub.lists)
	}
	if got := q.Entries()[0]; got.RevisionID != "revision" || pending(got) || hub.creates != 0 {
		t.Fatalf("location-only update modified content: %+v", got)
	}

	unknown := locationListing(Entry{Session: hubclient.V4Session{Repo: "example.org/new/project"}}, session.Location{ID: "old", Label: "old"}, 1)
	if unknown.Repo != "" {
		t.Fatalf("origin-less historical checkout acquired %q", unknown.Repo)
	}
	initial := q.Entries()[0]
	for _, state := range []string{"excluded", "session_deleted", "blocked", "unadmitted"} {
		e := initial
		switch state {
		case "excluded":
			e.Excluded = true
		case "session_deleted":
			e.FailureCode = state
		case "blocked":
			e.SourceBlocked = true
		case "unadmitted":
			e.SessionID = ""
		}
		if locationEligible(e) {
			t.Fatalf("%s passed location admission", state)
		}
	}
}
