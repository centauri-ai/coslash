package syncv4

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestGrokDiscoveryIdentityAndMetadata(t *testing.T) {
	entries := discoveredEntries([]*session.Session{
		{Agent: "grok", ID: "shared", DetailRevision: "grok-revision", StartedAt: 1, LastActivityTime: 2, UnattributedTokens: &session.ModelTokens{InputTokens: 12, OutputTokens: 3, CacheReadInputTokens: 4}},
		{Agent: "codex", ID: "shared"},
		{Agent: "grok", ID: "child", ParentSessionID: "shared"},
		{Agent: "pi", ID: "pi"},
		{Agent: "Grok", ID: "case"},
	}, "install", nil, map[string]string{"shared": "verified-source"}, nil)
	if len(entries) != 2 {
		t.Fatalf("eligible entries = %+v, want Grok and Codex", entries)
	}
	if entries[0].Key == entries[1].Key || entries[0].Selection.Agent != "grok" || entries[0].Selection.SessionID != "shared" {
		t.Fatalf("identity = %+v", entries)
	}
	meta := entries[0].Session
	if meta.Title != "Grok session" || meta.Agent != "grok" || meta.Tokens != 19 || meta.InstallID != "install" || meta.StartedAt == nil || meta.EndedAt == nil {
		t.Fatalf("metadata = %+v", meta)
	}
}

func grokQueueSource(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	t.Setenv("OPENCODE_DB", filepath.Join(home, "missing.db"))
	group := filepath.Join(home, ".grok", "sessions", "group")
	for _, member := range []struct{ id, parent, kind string }{
		{"root", "", "interactive"}, {"child", "root", "subagent"}, {"grandchild", "child", "subagent"}, {"fork", "root", "fork"}, {"worktree", "root", "worktree"},
	} {
		dir := filepath.Join(group, member.id)
		data, err := json.Marshal(map[string]any{"info": map[string]string{"id": member.id, "cwd": filepath.Join(home, "project")}, "chat_format_version": 1, "session_kind": member.kind, "parent_session_id": member.parent, "created_at": "2026-10-01T12:00:00Z", "updated_at": "2026-10-01T12:01:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		grokQueueWrite(t, filepath.Join(dir, "summary.json"), data)
		grokQueueWrite(t, filepath.Join(dir, "updates.jsonl"), []byte(`{"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"synthetic queue"}}}}`+"\n"))
		grokQueueWrite(t, filepath.Join(dir, "usage.json"), []byte(`{"session":{"inputTokens":12,"outputTokens":3,"cachedReadTokens":4}}`))
	}
	for _, link := range []struct{ parent, child string }{{"root", "child"}, {"child", "grandchild"}} {
		data, err := json.Marshal(map[string]string{"child_session_id": link.child, "status": "completed"})
		if err != nil {
			t.Fatal(err)
		}
		grokQueueWrite(t, filepath.Join(group, link.parent, "subagents", link.child, "meta.json"), data)
	}
	grokQueueWrite(t, filepath.Join(home, ".grok", "auth.json"), []byte("synthetic secret stays local"))
	return home, group
}

func grokQueueWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type grokQueueHub struct {
	mu sync.Mutex
	*planHub
	requests []hubclient.V4Create
	records  map[string]fullsessionv1.Record
}

func (h *grokQueueHub) V4ListBatch(ctx context.Context, items []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	results, err := h.planHub.V4ListBatch(ctx, items)
	for i, item := range items {
		for _, request := range h.requests {
			if request.Session.LocalKeyHash == item.LocalKeyHash {
				results[i].State = "existing"
			}
		}
	}
	return results, err
}
func (h *grokQueueHub) V4Create(_ context.Context, request hubclient.V4Create) (hubclient.V4Status, error) {
	h.requests = append(h.requests, request)
	return hubclient.V4Status{UploadID: fmt.Sprint(len(h.requests)), SessionID: "ses_" + request.Session.LocalKeyHash, State: "open", Missing: missing(request.Manifest, nil)}, nil
}
func (h *grokQueueHub) V4Status(_ context.Context, id string) (hubclient.V4Status, error) {
	i, err := strconv.Atoi(id)
	if err != nil {
		return hubclient.V4Status{}, err
	}
	request := h.requests[i-1]
	return hubclient.V4Status{UploadID: id, SessionID: "ses_" + request.Session.LocalKeyHash, State: "open", Missing: missing(request.Manifest, nil)}, nil
}
func (h *grokQueueHub) V4PutChunk(_ context.Context, _ string, _ hubclient.V4Missing, reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	record, err := fullsessionv1.Decode(data)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records[record.SessionID] = record
	return nil
}
func (h *grokQueueHub) V4Confirm(context.Context, string, ...hubclient.V4Missing) (hubclient.V4Status, error) {
	return hubclient.V4Status{State: "open"}, nil
}
func (h *grokQueueHub) V4Finalize(_ context.Context, id string) (hubclient.V4Status, error) {
	status, err := h.V4Status(context.Background(), id)
	status.State, status.RevisionID, status.Missing = "completed", "rev_"+id, nil
	return status, err
}

func TestGrokAutomaticQueueFamilyUpdatesAndRestart(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		t.Run(fmt.Sprint(streamed), func(t *testing.T) {
			home, group := grokQueueSource(t)
			queueRoot, spool := t.TempDir(), t.TempDir()
			queue, err := Open(queueRoot)
			if err != nil {
				t.Fatal(err)
			}
			plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
			hub := &grokQueueHub{planHub: &planHub{plan: plan}, records: map[string]fullsessionv1.Record{}}
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			newRunner := func() *Runner {
				manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool, LocalHome: func() (string, error) { return home, nil }})
				runner := &Runner{Queue: queue, Backup: manager, Hub: hub, Now: func() time.Time { return now }, RequireImportPlan: true,
					Discover: func(ctx context.Context) ([]*session.Session, error) {
						return inventory.DiscoverAll(ctx, inventory.DiscoverOptions{})
					}}
				if streamed {
					runner.DiscoverBatches = func(ctx context.Context, emit func(DiscoveryBatch) error) error {
						for batch, err := range inventory.Discover(ctx, inventory.DiscoverOptions{BatchFamilies: 1}) {
							if err != nil {
								return err
							}
							if err := emit(DiscoveryBatch{Sessions: batch.Sessions, ContentBytes: batch.ContentBytes, GrokFamilies: batch.GrokFamilies}); err != nil {
								return err
							}
						}
						return nil
					}
				}
				return runner
			}
			runner := newRunner()
			run := func() {
				t.Helper()
				runner.RequestDiscovery()
				if err := runner.SyncOnce(t.Context()); err != nil {
					t.Fatal(err)
				}
				now = now.Add(3 * time.Minute)
				if err := runner.SyncOnce(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			run()
			if len(queue.Entries()) != 3 || len(hub.requests) != 3 {
				t.Fatalf("roots=%+v uploads=%d", queue.Entries(), len(hub.requests))
			}
			for _, request := range hub.requests {
				if request.Session.Agent != "grok" || request.Session.Tokens != 19 {
					t.Fatalf("metadata=%+v", request.Session)
				}
				for _, artifact := range request.Manifest.Artifacts {
					if artifact.Kind != sessionbackupv1.KindParsedSessionRecord {
						t.Fatalf("raw upload: %+v", artifact)
					}
				}
			}
			for _, id := range []string{"fork", "worktree"} {
				if hub.records[id].ParentSessionID != "" {
					t.Fatalf("fork became child: %+v", hub.records[id])
				}
			}
			if hub.records["child"].ParentSessionID != "root" || hub.records["grandchild"].ParentSessionID != "child" {
				t.Fatal("recursive lineage lost")
			}
			beforeRoot, beforeChild := hub.records["root"].RevisionID, hub.records["child"].RevisionID
			beforeSource := ""
			for _, entry := range queue.Entries() {
				if entry.Selection.SessionID == "root" {
					beforeSource = entry.SourceRevision
				}
			}
			run()
			if len(hub.requests) != 3 {
				t.Fatal("unchanged discovery uploaded")
			}
			// A usage-only change must invalidate the family without forcing root churn.
			grokQueueWrite(t, filepath.Join(group, "grandchild", "usage.json"), []byte(`{"session":{"inputTokens":120,"outputTokens":3,"cachedReadTokens":4}}`))
			now = now.Add(time.Minute)
			run()
			if len(hub.requests) != 4 || hub.records["root"].RevisionID != beforeRoot || hub.records["child"].RevisionID == beforeChild || hub.records["grandchild"].Session.UnattributedTokens.InputTokens != 120 {
				t.Fatalf("usage update uploads=%d root=%s child=%s", len(hub.requests), hub.records["root"].RevisionID, hub.records["child"].RevisionID)
			}
			var root Entry
			for _, entry := range queue.Entries() {
				if entry.Selection.SessionID == "root" {
					root = entry
				}
			}
			if root.SourceRevision == beforeSource || root.SyncedSourceRevision != root.SourceRevision || root.RevisionID == "" {
				t.Fatalf("source not committed: %+v", root)
			}

			queue, err = Open(queueRoot)
			if err != nil {
				t.Fatal(err)
			}
			runner = newRunner()
			run()
			if len(hub.requests) != 4 {
				t.Fatal("restart resent unchanged records")
			}
			// A failed producer refresh preserves its last verified bundle. Accepted
			// uploads have already discarded their spool through the normal queue path.
			good, err := runner.Backup.Prepare(t.Context(), root.Selection)
			if err != nil {
				t.Fatal(err)
			}
			grokQueueWrite(t, filepath.Join(group, "grandchild", "usage.json"), []byte(`{"session":`))
			if _, err := runner.Backup.Prepare(t.Context(), root.Selection); err == nil {
				t.Fatal("malformed refresh prepared")
			}
			if _, err := runner.Backup.Open(good.BundleID); err != nil {
				t.Fatalf("last good lost: %v", err)
			}
			runner.RequestDiscovery()
			if err := runner.SyncOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			now = now.Add(3 * time.Minute)
			if err := runner.SyncOnce(t.Context()); err == nil {
				t.Fatal("malformed source did not fail preparation")
			}

			if len(hub.requests) != 4 {
				t.Fatal("failed prepare uploaded")
			}

			grokQueueWrite(t, filepath.Join(group, "grandchild", "usage.json"), []byte(`{"session":{"inputTokens":121}}`))
			now = now.Add(time.Minute)
			run()
			if len(hub.requests) != 5 {
				t.Fatal("repaired source did not retry")
			}
			beforeContent := hub.records["root"].RevisionID
			grokQueueWrite(t, filepath.Join(group, "root", "updates.jsonl"), []byte(`{"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"updated synthetic queue"}}}}`+"\n"))
			now = now.Add(time.Minute)
			run()
			if len(hub.requests) != 6 || hub.records["root"].RevisionID == beforeContent {
				t.Fatal("content update did not upload")
			}
		})
	}
}

func TestGrokForkEligibilityKeepsLocalLineage(t *testing.T) {
	fork := &session.Session{Agent: "grok", ID: "fork", ParentSessionID: "origin"}
	child := &session.Session{Agent: "grok", ID: "child", ParentSessionID: "origin"}
	entries := discoveredEntries([]*session.Session{fork, child}, "install", nil, map[string]string{"fork": "verified-source"}, nil)
	if len(entries) != 1 || entries[0].Selection.SessionID != "fork" || fork.ParentSessionID != "origin" {
		t.Fatalf("fork eligibility or local lineage: %+v %+v", entries, fork)
	}
}

func TestGrokV4HTTPResume(t *testing.T) {
	home, _ := grokQueueSource(t)
	spool := t.TempDir()
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool, LocalHome: func() (string, error) { return home, nil }})
	prepared, err := manager.Prepare(t.Context(), sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: "grok", SessionID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	name := "Fixture Grok"
	source := &session.Session{Agent: "grok", ID: "root", Name: &name, StartedAt: now.Add(-time.Hour).UnixMilli(), LastActivityTime: now.UnixMilli()}
	testV4HTTPResume(t, manager, prepared, spool, source, false)
}

func TestGrokUploadPolicyGuards(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	meta := hubclient.V4Session{Agent: "grok", Repo: "owner/private", CWDLabel: "~/private"}
	for _, tc := range []struct {
		name       string
		config     hubclient.V4Config
		age        time.Duration
		metered    bool
		battery    int
		localPause bool
		want       error
	}{
		{name: "pause", config: hubclient.V4Config{Paused: true}, battery: -1, want: ErrPaused},
		{name: "device off", config: hubclient.V4Config{DeviceOff: true}, battery: -1, want: ErrPaused},
		{name: "local pause", localPause: true, battery: -1, want: ErrPaused},
		{name: "stale consent", age: consentAge, battery: -1, want: ErrStaleConsent},
		{name: "metered", metered: true, battery: -1, want: ErrPaused},
		{name: "low battery", battery: 19, want: ErrPaused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &Runner{checkedAt: now.Add(-tc.age), Now: func() time.Time { return now }, config: tc.config, LocalPause: func() bool { return tc.localPause }, Conditions: func(context.Context) (bool, int, error) { return tc.metered, tc.battery, nil }}
			if err := runner.allowed(t.Context(), meta); !errors.Is(err, tc.want) {
				t.Fatalf("allowed=%v want %v", err, tc.want)
			}
		})
	}
	if !leftOut(meta, []string{"owner/*"}) || !leftOut(meta, []string{"~/private"}) {
		t.Fatal("Grok escaped exclusion")
	}
}

func TestGrokDiscoveryRequiresVerifiedRoot(t *testing.T) {
	entries := discoveredEntries([]*session.Session{{Agent: "grok", ID: "unverified"}}, "install", nil, map[string]string{}, nil)
	if len(entries) != 0 {
		t.Fatalf("unverified root entered queue: %+v", entries)
	}
}

func TestGrokQueueRecentChildOldParentWindowAndCap(t *testing.T) {
	home, group := grokQueueSource(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	// A valid discovery summary outside the window need not have upload inputs.
	data, err := json.Marshal(map[string]any{"info": map[string]string{"id": "old", "cwd": filepath.Join(home, "project")}, "chat_format_version": 1, "created_at": old.Format(time.RFC3339), "updated_at": old.Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(group, "old", "summary.json")
	grokQueueWrite(t, oldPath, data)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}
	for id, stamp := range map[string]time.Time{"root": old, "child": now.Add(-time.Hour)} {
		path := filepath.Join(group, id, "summary.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var summary map[string]any
		if err := json.Unmarshal(data, &summary); err != nil {
			t.Fatal(err)
		}
		summary["created_at"], summary["updated_at"] = stamp.Format(time.RFC3339), stamp.Format(time.RFC3339)
		data, err = json.Marshal(summary)
		if err != nil {
			t.Fatal(err)
		}
		grokQueueWrite(t, path, data)
	}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 1, MaxSessionsPerAgent: 1}
	hub := &grokQueueHub{planHub: &planHub{plan: plan}, records: map[string]fullsessionv1.Record{}}
	runner := &Runner{Queue: queue, Hub: hub, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir(), LocalHome: func() (string, error) { return home, nil }}), Now: func() time.Time { return now }, RequireImportPlan: true,
		Discover: func(context.Context) ([]*session.Session, error) { t.Fatal("used unbounded fallback"); return nil, nil },
		DiscoverBatches: func(ctx context.Context, emit func(DiscoveryBatch) error) error {
			for batch, err := range inventory.Discover(ctx, inventory.DiscoverOptions{MinActivityMs: now.Add(-3 * 24 * time.Hour).UnixMilli(), BatchFamilies: 1}) {
				if err != nil {
					return err
				}
				if err := emit(DiscoveryBatch{Sessions: batch.Sessions, ContentBytes: batch.ContentBytes, GrokFamilies: batch.GrokFamilies}); err != nil {
					return err
				}
			}
			return nil
		}}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Minute)
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(hub.requests) != 1 || len(hub.records) != 3 || hub.records["root"].SessionID != "root" || hub.records["child"].ParentSessionID != "root" {
		t.Fatalf("capped recent family: uploads=%d records=%+v", len(hub.requests), hub.records)
	}
	for _, entry := range queue.Entries() {
		if entry.Selection.SessionID == "old" {
			t.Fatal("out-of-window history entered queue")
		}
	}
}

func TestGrokIncompleteAccountingDiscoveryAndMetadata(t *testing.T) {
	zero, three := 0.0, 3.0
	for _, tc := range []struct {
		name, usage               string
		rows                      int
		tokens                    int64
		unavailable, unattributed bool
		cost                      *float64
	}{
		{name: "positive missing contribution", usage: `{"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000},"grok-4.7":{"costUsdTicks":20000000000}}}`, rows: 1, unavailable: true},
		{name: "negative missing contribution", usage: `{"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000},"grok-4.7":{"costUsdTicks":-10000000000}}}`, rows: 1, unavailable: true},
		{name: "zero missing contribution", usage: `{"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000},"grok-4.7":{"costUsdTicks":0}}}`, rows: 1, unavailable: true},
		{name: "authoritative cost", usage: `{"costUsdTicks":30000000000,"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000},"grok-4.7":{"costUsdTicks":20000000000}}}`, rows: 1, unavailable: true, cost: &three},
		{name: "authoritative zero cost", usage: `{"costUsdTicks":0,"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000},"grok-4.7":{"costUsdTicks":20000000000}}}`, rows: 1, unavailable: true, cost: &zero},
		{name: "partial source cost", usage: `{"costUsdTicks":30000000000,"costIsPartial":true,"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000},"grok-4.7":{"costUsdTicks":20000000000}}}`, rows: 1, unavailable: true},
		{name: "incomplete source cost", usage: `{"costUsdTicks":30000000000,"usageIsIncomplete":true,"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000},"grok-4.7":{"costUsdTicks":20000000000}}}`, rows: 1, unavailable: true},
		{name: "unrepresented mixed token aggregate", usage: `{"inputTokens":19,"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000},"grok-4.7":{"costUsdTicks":20000000000}}}`, rows: 1, unavailable: true},
		{name: "represented source token aggregate", usage: `{"inputTokens":12,"outputTokens":3,"cachedReadTokens":4,"modelUsage":{"grok-4.7":{"costUsdTicks":20000000000}}}`, tokens: 19, unattributed: true},
		{name: "incomplete source token aggregate", usage: `{"inputTokens":19,"usageIsIncomplete":true,"modelUsage":{"grok-4.7":{"costUsdTicks":20000000000}}}`, unavailable: true, unattributed: true},
		{name: "represented zero token aggregate", usage: `{"inputTokens":0,"modelUsage":{"grok-4.7":{"costUsdTicks":20000000000}}}`, unattributed: true},
		{name: "represented tokens and source cost", usage: `{"inputTokens":19,"costUsdTicks":30000000000,"modelUsage":{"grok-4.7":{"costUsdTicks":20000000000}}}`, tokens: 19, unattributed: true, cost: &three},
		{name: "counterless cost only", usage: `{"modelUsage":{"grok-4.7":{"costUsdTicks":20000000000}}}`, unavailable: true},
	} {
		for _, member := range []string{"root", "child", "grandchild"} {
			t.Run(tc.name+"/"+member, func(t *testing.T) {
				_, group := grokQueueSource(t)
				for _, id := range []string{"root", "child", "grandchild"} {
					grokQueueWrite(t, filepath.Join(group, id, "usage.json"), []byte(`{"session":{"modelUsage":{"gpt-5":{"inputTokens":12,"outputTokens":3,"costUsdTicks":10000000000}}}}`))
				}
				grokQueueWrite(t, filepath.Join(group, member, "usage.json"), []byte(`{"session":`+tc.usage+`}`))
				check := func(id string, value *session.Session) {
					t.Helper()
					rows, tokens, unavailable, unattributed := 1, int64(15), false, false
					cost := 1.0
					wantCost := &cost
					if id == member {
						rows, tokens, unavailable, unattributed, wantCost = tc.rows, tc.tokens, tc.unavailable, tc.unattributed, tc.cost
					}
					if value.TokensUnavailable != unavailable || value.CostUnavailable != (wantCost == nil) || (value.UnattributedTokens != nil) != unattributed || len(value.Tokens) != rows || !reflect.DeepEqual(value.Cost, wantCost) {
						t.Fatalf("%s aggregate availability: tokensUnavailable=%v costUnavailable=%v rows=%d unattributed=%v cost=%v", id, value.TokensUnavailable, value.CostUnavailable, len(value.Tokens), value.UnattributedTokens != nil, value.Cost)
					}
					if rows == 1 {
						used := value.Tokens["gpt-5"]
						if used.InputTokens != 12 || used.OutputTokens != 3 || used.Cost != 1 {
							t.Fatalf("%s known model facts changed: %+v", id, used)
						}
					}
					meta := sessionMetadata(value, "synthetic-l5")
					if meta.Tokens != tokens || wantCost == nil && meta.CostMicroUSD != 0 || wantCost != nil && meta.CostMicroUSD != int64(*wantCost*1000000) {
						t.Fatalf("%s listing metadata tokens=%d cost=%d", id, meta.Tokens, meta.CostMicroUSD)
					}
				}
				roots, err := inventory.DiscoverAll(t.Context(), inventory.DiscoverOptions{})
				if err != nil {
					t.Fatal(err)
				}
				var root *session.Session
				for _, value := range roots {
					if value.Agent == "grok" && value.ID == "root" {
						root = value
					}
				}
				if root == nil {
					t.Fatal("live root hidden")
				}
				check("root", root)
				parsed, metadata, err := grok.CollectContext(t.Context(), 0)
				if err != nil {
					t.Fatal(err)
				}
				family, err := collector.ComposePortableContext(t.Context(), vendors.LocalReadSource, map[string]vendors.RemoteCollection{"grok": {Sessions: parsed, Metadata: metadata}})
				if err != nil {
					t.Fatal(err)
				}
				byID := map[string]*session.Session{}
				for _, item := range family {
					byID[item.Session.ID] = item.Session
				}
				for _, id := range []string{"root", "child", "grandchild"} {
					if byID[id] == nil {
						t.Fatalf("member %s hidden", id)
					}
					check(id, byID[id])
				}
				for _, value := range []*session.Session{root, byID["root"], byID["child"]} {
					for _, child := range value.Subagents {
						own := byID[child.ID]
						if own == nil || child.TokensUnavailable != own.TokensUnavailable || child.CostUnavailable != own.CostUnavailable || !reflect.DeepEqual(child.Cost, own.Cost) || !reflect.DeepEqual(child.Tokens, own.Tokens) || !reflect.DeepEqual(child.UnattributedTokens, own.UnattributedTokens) {
							t.Fatal("direct child projection lost accounting availability or facts")
						}
					}
				}
			})
		}
	}
}

func TestGrokMetadataHonorsAccountingUnavailable(t *testing.T) {
	cost := 1.0
	value := &session.Session{Agent: "grok", Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 12, OutputTokens: 3}}, TokensUnavailable: true, CostUnavailable: true, Cost: &cost}
	meta := sessionMetadata(value, "synthetic-l5")
	if meta.Tokens != 0 || meta.CostMicroUSD != 0 {
		t.Fatalf("stale unavailable totals listed: %+v", meta)
	}
	value.CostUnavailable = false
	meta = sessionMetadata(value, "synthetic-l5")
	if meta.Tokens != 0 || meta.CostMicroUSD != 1000000 {
		t.Fatalf("independent source cost lost: %+v", meta)
	}
}
