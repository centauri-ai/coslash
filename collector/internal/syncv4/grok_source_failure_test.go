package syncv4

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func TestGrokSourceFailurePreservesPendingAndHealthyDiscovery(t *testing.T) {
	for _, batchSize := range []int{0, 1, 64} {
		for _, failure := range []string{"oversize", "missing"} {
			t.Run(fmt.Sprintf("batch-%d/%s", batchSize, failure), func(t *testing.T) {
				home, group := grokQueueSource(t)
				now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
				queueRoot, spool := t.TempDir(), t.TempDir()
				queue, err := Open(queueRoot)
				if err != nil {
					t.Fatal(err)
				}
				manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool, LocalHome: func() (string, error) { return home, nil }})
				plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
				hub := &grokQueueHub{planHub: &planHub{plan: plan}, records: map[string]fullsessionv1.Record{}}
				runner := &Runner{Version: "source-failure-test", Queue: queue, Backup: manager, Hub: hub, Now: func() time.Time { return now }, RequireImportPlan: true,
					Discover: func(ctx context.Context) ([]*session.Session, error) {
						return inventory.DiscoverAll(ctx, inventory.DiscoverOptions{})
					}}
				if batchSize > 0 {
					runner.DiscoverBatches = func(ctx context.Context, emit func(DiscoveryBatch) error) error {
						for batch, err := range inventory.Discover(ctx, inventory.DiscoverOptions{BatchFamilies: batchSize}) {
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
				sessions, err := inventory.DiscoverAll(t.Context(), inventory.DiscoverOptions{})
				if err != nil {
					t.Fatal(err)
				}
				var root *session.Session
				for _, item := range sessions {
					if item.Agent == "grok" && item.ID == "root" {
						root = item
					}
				}
				if root == nil {
					t.Fatal("root missing")
				}
				prepared, err := manager.Prepare(t.Context(), sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: "grok", SessionID: "root"})
				if err != nil {
					t.Fatal(err)
				}
				// Retain a genuinely verified pending capture with its actual source hash.
				var refsRoot string
				for batch, err := range inventory.Discover(t.Context(), inventory.DiscoverOptions{}) {
					if err != nil {
						t.Fatal(err)
					}
					for _, family := range batch.GrokFamilies {
						if family.ID == "root" {
							refsRoot, err = sessionbackupproducer.GrokSourceRevision(t.Context(), family)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				pendingRoot := discoveredEntries([]*session.Session{root}, queue.InstallID(), nil, map[string]string{"root": refsRoot})[0]
				manifest, err := runner.manifest(prepared)
				if err != nil {
					t.Fatal(err)
				}
				pendingRoot.BundleID, pendingRoot.ContentSHA256, pendingRoot.Manifest = prepared.BundleID, manifest.ContentSHA256, &manifest
				if err := queue.Merge([]Entry{pendingRoot}, now); err != nil {
					t.Fatal(err)
				}
				pendingRoot = queue.Entries()[0]
				// Add a real existing-agent source after the pending Grok capture exists.
				meta, err := json.Marshal(map[string]any{"timestamp": "2026-10-01T12:00:00Z", "type": "session_meta", "payload": map[string]string{"id": fixtureRootID, "cwd": filepath.Join(home, "project")}})
				if err != nil {
					t.Fatal(err)
				}
				rollout := string(meta) + "\n" + `{"timestamp":"2026-10-01T12:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"healthy codex"}}` + "\n" + `{"timestamp":"2026-10-01T12:00:02Z","type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
				grokQueueWrite(t, filepath.Join(codex.SessionsRoot(home), "rollout-"+fixtureRootID+".jsonl"), []byte(rollout))
				badPath := filepath.Join(group, "grandchild", "usage.json")
				if failure == "missing" {
					badPath = filepath.Join(group, "grandchild", "updates.jsonl")
				}
				original, err := os.ReadFile(badPath)
				if err != nil {
					t.Fatal(err)
				}
				if failure == "oversize" {
					file, err := os.OpenFile(badPath, os.O_WRONLY|os.O_TRUNC, 0o600)
					if err != nil {
						t.Fatal(err)
					}
					truncateErr := file.Truncate((64 << 20) + 1)
					closeErr := file.Close()
					if err := errors.Join(truncateErr, closeErr); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(badPath); err != nil {
					t.Fatal(err)
				}
				checkBlocked := func(wantCode string) {
					t.Helper()
					if len(queue.Entries()) != 4 {
						t.Fatalf("healthy discovery starved: %+v", queue.Entries())
					}
					var blocked Entry
					for _, entry := range queue.Entries() {
						if entry.Selection.Agent == "grok" && entry.Selection.SessionID == "root" {
							blocked = entry
						}
					}
					if !pending(blocked) || blocked.RevisionID != "" || blocked.SourceRevision != pendingRoot.SourceRevision || blocked.BundleID != pendingRoot.BundleID || blocked.ContentSHA256 != pendingRoot.ContentSHA256 || !reflect.DeepEqual(blocked.Manifest, pendingRoot.Manifest) {
						t.Fatalf("verified pending state changed: %+v", blocked)
					}
					if queue.Matches(pendingRoot) {
						t.Fatal("stale pending work remained uploadable")
					}
					if blocked.FailureCode != wantCode {
						t.Fatalf("source failure code=%q want=%q", blocked.FailureCode, wantCode)
					}
					if wantCode != "" && blocked.LoggedFailure != wantCode+" "+blocked.SessionID {
						t.Fatalf("source failure log classification=%q", blocked.LoggedFailure)
					}
					if _, err := manager.Open(prepared.BundleID); err != nil {
						t.Fatalf("last good bundle lost: %v", err)
					}
					if _, ok := hub.records["root"]; ok {
						t.Fatal("blocked pending family uploaded")
					}
				}
				if err := runner.SyncOnce(t.Context()); err == nil {
					t.Fatal("source failure not reported")
				}
				wantCode := "unreadable_source"
				if failure == "oversize" {
					wantCode = "too_large"
				}
				checkBlocked(wantCode)
				for _, entry := range queue.Entries() {
					if entry.Selection.SessionID == "root" {
						ok, err := queue.RetrySession(entry.SessionID)
						if err != nil || !ok {
							t.Fatalf("retry blocked source: %v %v", ok, err)
						}
					}
				}
				checkBlocked("")

				// Reopen the same queue and vary Local version: neither may release a bad source.
				queue, err = Open(queueRoot)
				if err != nil {
					t.Fatal(err)
				}
				runner.Queue = queue
				runner.Version = "source-failure-restarted"
				now = now.Add(10 * time.Minute)
				runner.RequestDiscovery()
				if err := runner.SyncOnce(t.Context()); err == nil {
					t.Fatal("persistent source failure not reported")
				}
				checkBlocked(wantCode)
				if len(hub.records) != 3 || hub.records["fork"].SessionID != "fork" || hub.records["worktree"].SessionID != "worktree" || hub.records[fixtureRootID].Agent != "codex" {
					t.Fatalf("healthy sources did not upload: %+v", hub.records)
				}
				// Repair to the exact earlier bytes, not a fabricated new revision.
				grokQueueWrite(t, badPath, original)
				now = now.Add(10 * time.Minute)
				runner.RequestDiscovery()
				if err := runner.SyncOnce(t.Context()); err != nil {
					t.Fatal(err)
				}
				now = now.Add(3 * time.Minute)
				if err := runner.SyncOnce(t.Context()); err != nil {
					t.Fatal(err)
				}
				if hub.records["root"].SessionID != "root" || hub.records["grandchild"].ParentSessionID != "child" {
					t.Fatal("repaired pending family did not resume")
				}
				count := len(hub.requests)
				runner.RequestDiscovery()
				if err := runner.SyncOnce(t.Context()); err != nil {
					t.Fatal(err)
				}
				if len(hub.requests) != count {
					t.Fatal("unchanged repaired family resent")
				}
			})
		}
	}
}

func TestGrokMetadataCheckedCounters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tokens session.ModelTokens
		want   int64
	}{
		{"input max", session.ModelTokens{InputTokens: math.MaxInt64}, math.MaxInt64},
		{"output max", session.ModelTokens{OutputTokens: math.MaxInt64}, math.MaxInt64},
		{"cache creation max", session.ModelTokens{CacheCreationInputTokens: math.MaxInt64}, math.MaxInt64},
		{"one hour max", session.ModelTokens{CacheCreation1hInputTokens: math.MaxInt64}, math.MaxInt64},
		{"cache read max", session.ModelTokens{CacheReadInputTokens: math.MaxInt64}, math.MaxInt64},
		{"all five", session.ModelTokens{InputTokens: 1, OutputTokens: 2, CacheCreationInputTokens: 3, CacheCreation1hInputTokens: 4, CacheReadInputTokens: 5}, 15},
		{"max plus one", session.ModelTokens{InputTokens: math.MaxInt64, OutputTokens: 1}, 0},
		{"positive wrap", session.ModelTokens{InputTokens: math.MaxInt64, OutputTokens: math.MaxInt64, CacheReadInputTokens: 3}, 0},
		{"negative", session.ModelTokens{InputTokens: -1, OutputTokens: 3}, 0},
		{"explicit zero", session.ModelTokens{}, 0},
	} {
		for _, attributed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", tc.name, attributed), func(t *testing.T) {
				item := &session.Session{Agent: "grok", ID: "metadata"}
				if attributed {
					item.Tokens = map[string]session.ModelTokens{"existing-model": tc.tokens}
				} else {
					item.UnattributedTokens = &tc.tokens
				}
				if got := sessionMetadata(item, "install").Tokens; got != tc.want {
					t.Fatalf("tokens=%d want %d", got, tc.want)
				}
			})
		}
	}
	item := &session.Session{Agent: "grok", ID: "models", Tokens: map[string]session.ModelTokens{"first": {InputTokens: math.MaxInt64}, "second": {OutputTokens: math.MaxInt64, CacheReadInputTokens: 3}}}
	if got := sessionMetadata(item, "install").Tokens; got != 0 {
		t.Fatalf("accumulated models wrapped to %d", got)
	}
}

func TestGrokMetadataOverflowStillUploadsExactCounters(t *testing.T) {
	home, group := grokQueueSource(t)
	grokQueueWrite(t, filepath.Join(group, "root", "usage.json"), []byte(`{"session":{"inputTokens":9223372036854775807,"outputTokens":9223372036854775807,"cachedReadTokens":3}}`))
	spool := t.TempDir()
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool, LocalHome: func() (string, error) { return home, nil }})
	prepared, err := manager.Prepare(t.Context(), sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: "grok", SessionID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	var canonical fullsessionv1.Record
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.Kind == sessionbackupv1.KindParsedSessionRecord && artifact.MemberID == "root" {
			data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
			if err != nil {
				t.Fatal(err)
			}
			canonical, err = fullsessionv1.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := &grokQueueHub{planHub: &planHub{plan: &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}}, records: map[string]fullsessionv1.Record{}}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	runner := &Runner{Queue: queue, Hub: hub, Backup: manager, Now: func() time.Time { return now }, Discover: func(ctx context.Context) ([]*session.Session, error) {
		return inventory.DiscoverAll(ctx, inventory.DiscoverOptions{})
	}}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Minute)
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, request := range hub.requests {
		if request.Session.LocalKeyHash == localKey("local", "grok", "root") {
			found = true
			if request.Session.Tokens != 0 {
				t.Fatalf("overflow metadata=%d", request.Session.Tokens)
			}
		}
	}
	if !found {
		t.Fatal("valid portable family blocked by metadata overflow")
	}
	sent := hub.records["root"]
	if canonical.RevisionID == "" || sent.RevisionID != canonical.RevisionID || !reflect.DeepEqual(sent.Session.UnattributedTokens, canonical.Session.UnattributedTokens) {
		t.Fatal("canonical counter bytes changed")
	}
}

func TestGrokSourceFailureControlErrorsPropagate(t *testing.T) {
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, ErrPaused, ErrPolicyBlocked, ErrDeviceSyncOff, hubclient.V4Problem{Code: "sync_paused"}} {
		t.Run(failure.Error(), func(t *testing.T) {
			queue, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			entry := Entry{Key: "verified", Selection: sessionbackupproducer.Selection{Agent: "grok", SessionID: "root"}, Session: hubclient.V4Session{Agent: "grok"}, SourceRevision: "verified-source"}
			if err := queue.Merge([]Entry{entry}, time.Unix(1, 0)); err != nil {
				t.Fatal(err)
			}
			before := queue.Entries()
			runner := &Runner{Queue: queue}
			err = runner.reconcileGrokSources(nil, map[string]error{"root": fmt.Errorf("source check: %w", failure)})
			if !errors.Is(err, failure) {
				t.Fatalf("control identity=%v", err)
			}
			if !reflect.DeepEqual(before, queue.Entries()) {
				t.Fatal("control error changed pending state")
			}
		})
	}
}

type grokSourceRetryHub struct {
	*grokQueueHub
	delivered bool
}

func (h *grokSourceRetryHub) V4CheckIn(ctx context.Context, q hubclient.V4Queue, v int64, results []hubclient.V4CommandResult, agents []string, logs []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	out, err := h.planHub.V4CheckIn(ctx, q, v, results, agents, logs)
	if !h.delivered {
		h.delivered = true
		out.Commands = []hubclient.V4Command{{ID: "source_retry", Type: "retry", Payload: json.RawMessage(`{"sessionId":"ses_` + localKey("local", "grok", "root") + `"}`)}}
	}
	return out, err
}

func TestGrokSourceBlockedHubRetryVerifiesBeforeSettlement(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		for _, repaired := range []bool{false, true} {
			t.Run(fmt.Sprintf("streamed-%v/repaired-%v", streamed, repaired), func(t *testing.T) {
				home, group := grokQueueSource(t)
				queue, err := Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
				manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir(), LocalHome: func() (string, error) { return home, nil }})
				sessions, err := inventory.DiscoverAll(t.Context(), inventory.DiscoverOptions{})
				if err != nil {
					t.Fatal(err)
				}
				revisions, failures, err := sessionbackupproducer.LocalGrokSourceRevisions(t.Context())
				if err != nil || len(failures) != 0 {
					t.Fatalf("initial hash: %v %v", failures, err)
				}
				var entry Entry
				for _, item := range discoveredEntries(sessions, queue.InstallID(), nil, revisions) {
					if item.Selection.SessionID == "root" {
						entry = item
					}
				}
				if entry.Key == "" {
					t.Fatal("missing root")
				}
				prepared, err := manager.Prepare(t.Context(), entry.Selection)
				if err != nil {
					t.Fatal(err)
				}
				manifest, err := (&Runner{Backup: manager}).manifest(prepared)
				if err != nil {
					t.Fatal(err)
				}
				entry.BundleID, entry.ContentSHA256, entry.Manifest = prepared.BundleID, manifest.ContentSHA256, &manifest
				entry.SessionID, entry.UploadID = "ses_"+entry.Key, "1"
				if err := queue.Merge([]Entry{entry}, now); err != nil {
					t.Fatal(err)
				}
				entry = queue.Entries()[0]
				hub := &grokSourceRetryHub{grokQueueHub: &grokQueueHub{planHub: &planHub{plan: &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}}, records: map[string]fullsessionv1.Record{}, requests: []hubclient.V4Create{{Session: entry.Session, Manifest: manifest}}}}
				scans := 0
				runner := &Runner{Version: "retry", Queue: queue, Backup: manager, Hub: hub, Now: func() time.Time { return now }, RequireImportPlan: true, Discover: func(ctx context.Context) ([]*session.Session, error) {
					scans++
					return inventory.DiscoverAll(ctx, inventory.DiscoverOptions{})
				}}
				if streamed {
					runner.DiscoverBatches = func(ctx context.Context, emit func(DiscoveryBatch) error) error {
						scans++
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
				badPath := filepath.Join(group, "grandchild", "usage.json")
				original, err := os.ReadFile(badPath)
				if err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(badPath, os.O_WRONLY|os.O_TRUNC, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				truncateErr := file.Truncate((64 << 20) + 1)
				if err := errors.Join(truncateErr, file.Close()); err != nil {
					t.Fatal(err)
				}
				_, failures, err = sessionbackupproducer.LocalGrokSourceRevisions(t.Context())
				if err != nil || failures["root"] == nil {
					t.Fatalf("expected failed source: %v %v", failures, err)
				}
				if err := runner.reconcileGrokSources(nil, failures); err != nil {
					t.Fatal(err)
				}
				if repaired {
					grokQueueWrite(t, badPath, original)
				}
				// Simulate a just-finished scan: the retry must bypass discovery cooldown.
				runner.lastDiscoveryAt = now
				if err := runner.SyncOnce(t.Context()); !errors.Is(err, ErrCommandPickedUp) {
					t.Fatalf("pickup=%v", err)
				}
				blocked := queue.Entries()[0]
				if !blocked.SourceBlocked || queue.Matches(entry) || blocked.BundleID != entry.BundleID || blocked.UploadID != entry.UploadID || blocked.ContentSHA256 != entry.ContentSHA256 || !reflect.DeepEqual(blocked.Manifest, entry.Manifest) {
					t.Fatalf("retry released/changed capture: %+v", blocked)
				}
				if blocked.FailureCode != "" {
					t.Fatalf("old failure retained by new retry: %s", blocked.FailureCode)
				}
				for _, result := range queue.Results() {
					if result.Result != "in_progress" {
						t.Fatalf("settled before verification: %+v", result)
					}
				}
				if _, err := manager.Open(entry.BundleID); err != nil {
					t.Fatal(err)
				}
				// No clock advance or manual RequestDiscovery: normal retry wake must scan.
				syncErr := runner.SyncOnce(t.Context())
				if scans != 1 {
					t.Fatalf("retry verification scans=%d", scans)
				}
				if !repaired {
					if syncErr == nil {
						t.Fatal("fresh failure not reported")
					}
					var current Entry
					for _, item := range queue.Entries() {
						if item.Key == entry.Key {
							current = item
						}
					}
					if !current.SourceBlocked || current.FailureCode != "too_large" || current.UploadID != entry.UploadID || current.BundleID != entry.BundleID || !reflect.DeepEqual(current.Manifest, entry.Manifest) {
						t.Fatalf("bad source released: %+v", current)
					}
					if _, ok := hub.records["root"]; ok {
						t.Fatal("unsafe family sent")
					}
				} else {
					if syncErr != nil {
						t.Fatal(syncErr)
					}
					now = now.Add(3 * time.Minute)
					if err := runner.SyncOnce(t.Context()); err != nil {
						t.Fatal(err)
					}
					if hub.records["root"].SessionID != "root" || hub.records["grandchild"].ParentSessionID != "child" {
						t.Fatal("verified repaired capture did not resume")
					}
					if len(hub.requests) != 3 {
						t.Fatalf("retained upload recreated: requests=%d", len(hub.requests))
					}
				}
				want := "failed"
				if repaired {
					want = "done"
				}
				results := queue.Results()
				if len(results) != 1 || results[0].CommandID != "source_retry" || results[0].Result != want {
					t.Fatalf("settlement=%+v want=%s", results, want)
				}
				if err := runner.refreshConsent(t.Context()); err != nil {
					t.Fatal(err)
				}
				if len(hub.results) != 1 || hub.results[0].Result != want {
					t.Fatalf("check-in results=%+v", hub.results)
				}
			})
		}
	}
}

func TestGrokNestedOversizeBlocksPendingRoot(t *testing.T) {
	_, group := grokQueueSource(t)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "root", Selection: sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: "grok", SessionID: "root"}, Session: hubclient.V4Session{Agent: "grok"}, SourceRevision: "previously-verified", BundleID: "retained-bundle"}
	if err := queue.Merge([]Entry{entry}, time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	entry = queue.Entries()[0]
	if err := os.Remove(filepath.Join(group, "child", "subagents", "grandchild", "meta.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(group, "child", "updates.jsonl"), sessionbackupv1.MaxArtifactBytes+1); err != nil {
		t.Fatal(err)
	}
	revisions, failures, err := sessionbackupproducer.LocalGrokSourceRevisions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Queue: queue, Version: "nested-lineage-test"}
	if err := runner.reconcileGrokSources(revisions, failures); err != nil {
		t.Fatal(err)
	}
	t.Logf("failure IDs=%v root SourceBlocked=%v Matches=%v", failures, queue.Entries()[0].SourceBlocked, queue.Matches(entry))
	if queue.Matches(entry) {
		t.Fatal("pending root remains uploadable despite oversized nested lineage")
	}
}
