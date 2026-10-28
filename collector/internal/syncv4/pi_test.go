package syncv4

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func piQueueSource(t *testing.T) (string, string, []byte) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_HOME", filepath.Join(home, "coslash"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("OPENCODE_DB", filepath.Join(home, "missing.db"))
	t.Setenv("GROK_HOME", filepath.Join(home, "grok"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("COSLASH_SYNC_PAUSED", "")
	t.Chdir(home)
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": "pi.custom_01", "cwd": home, "timestamp": "2026-10-01T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	data := append(header, []byte("\n"+`{"type":"message","id":"u","timestamp":"2026-10-01T12:00:01Z","message":{"role":"user","content":"synthetic Pi queue"}}`+"\n"+`{"type":"message","id":"a","parentId":"u","timestamp":"2026-10-01T12:00:02Z","message":{"role":"assistant","model":"m","content":"first branch","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":1}}}}`+"\n"+`{"type":"message","id":"b","parentId":"u","timestamp":"2026-10-01T12:00:03Z","message":{"role":"assistant","model":"m","content":"last branch","usage":{"input":2,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.2}}}}`+"\n")...)
	root, err := pi.Root()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "unrelated-name.jsonl")
	grokQueueWrite(t, path, data)
	return home, path, data
}

func TestPiDiscoveryIdentityAndMixedMetadata(t *testing.T) {
	entries := discoveredEntries([]*session.Session{
		{Agent: "pi", ID: "shared", DetailRevision: "detail", Tokens: map[string]session.ModelTokens{"m": {InputTokens: 10, OutputTokens: 5}}, UnattributedTokens: &session.ModelTokens{InputTokens: 2, OutputTokens: 1}},
		{Agent: "codex", ID: "shared"}, {Agent: "pi", ID: "unverified"},
	}, "install", nil, nil, map[string]string{"shared": "native-revision"})
	if len(entries) != 2 || entries[0].Key == entries[1].Key || entries[0].SourceRevision != "native-revision" || entries[0].Session.Title != "Pi session" || entries[0].Session.Tokens != 18 {
		t.Fatalf("Pi eligibility/metadata = %+v", entries)
	}
}

func TestPiRevisionCandidatesApplyWindowAndCapsWithoutMutation(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 2, MaxSessionsPerAgent: 1}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	entry := func(agent, id string, age time.Duration) Entry {
		return Entry{Key: id, Selection: sessionbackupproducer.Selection{Agent: agent, SessionID: id}, Session: hubclient.V4Session{Agent: agent}, Activity: now.Add(-age).UnixMilli()}
	}
	if err := q.Merge([]Entry{entry("codex", "other", time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	before := q.Entries()
	selected := q.piRevisionCandidates([]Entry{entry("pi", "newest", 2*time.Hour), entry("pi", "older", 3*time.Hour), entry("pi", "outside", 10*24*time.Hour)})
	if len(selected) != 1 || selected[0].Key != "newest" || !reflect.DeepEqual(before, q.Entries()) {
		t.Fatalf("revision candidates = %+v, queue = %+v", selected, q.Entries())
	}
}

func TestPiLiveOnlyRechecksImportedSelectionAfterOwnerEnds(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	plan := &hubclient.V4ImportPlan{Version: 2, Window: "3d"}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: plan}}, now); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	q.state.LiveOnlyPlanVersion, q.state.CatchUpFrozenVersion = 2, 2
	q.mu.Unlock()
	entry := Entry{Key: "imported", Selection: sessionbackupproducer.Selection{Agent: "pi", SessionID: "imported"}, Session: hubclient.V4Session{Agent: "pi"}, Activity: now.Add(-30 * 24 * time.Hour).UnixMilli(), RevisionID: "accepted", SourceRevision: "prior"}
	unimported := entry
	unimported.Key, unimported.Selection.SessionID, unimported.RevisionID = "older-local", "older-local", ""
	selected := q.piRevisionCandidates([]Entry{entry, unimported})
	if len(selected) != 1 || selected[0].Key != "imported" {
		t.Fatalf("live-only revision candidates = %+v", selected)
	}
}

func TestPiSourceFailurePreservesPendingCaptureUntilRepair(t *testing.T) {
	_, path, valid := piQueueSource(t)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	config := hubclient.V4Config{ImportPlan: plan}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: config}, now); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: localKey("local", "pi", "pi.custom_01"), Selection: sessionbackupproducer.Selection{SourceKind: "local", SourceID: "local", Agent: "pi", SessionID: "pi.custom_01"}, Session: hubclient.V4Session{Agent: "pi"}, Activity: now.Add(-time.Hour).UnixMilli(), SourceRevision: "last-good", BundleID: "bundle", UploadID: "upload", ContentSHA256: "content"}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	grokQueueWrite(t, path, append(append([]byte(nil), valid...), []byte("{invalid}\n")...))
	sources, err := pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Queue: q, config: config, checkedAt: now, Now: func() time.Time { return now }, Version: "test"}
	if err := runner.syncPiSources(t.Context(), sources, nil, map[string]string{}, nil); err == nil {
		t.Fatal("malformed source did not fail")
	}
	blocked := q.Entries()[0]
	if !blocked.SourceBlocked || blocked.BundleID != "bundle" || blocked.UploadID != "upload" || blocked.SourceRevision != "last-good" || blocked.FailureCode != "unreadable_source" || blocked.LoggedFailure != "malformed_artifact " || blocked.ParkedVersion != "test" || len(q.Discards()) != 0 || q.Matches(entry) {
		t.Fatalf("lost verified pending capture: %+v", blocked)
	}
	grokQueueWrite(t, path, valid)
	sources, err = pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.syncPiSources(t.Context(), sources, nil, map[string]string{}, nil); err != nil {
		t.Fatal(err)
	}
	current := q.Entries()[0]
	if current.SourceBlocked || current.SourceRevision == "last-good" || current.SourceRevision == "" || current.BundleID != "" || current.UploadID != "" {
		t.Fatalf("repair did not replace stale capture: %+v", current)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runner.syncPiSources(ctx, sources, nil, map[string]string{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("revision cancellation = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	sources, err = pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.syncPiSources(t.Context(), sources, nil, map[string]string{}, nil); err == nil {
		t.Fatal("absent queued source was treated as verified")
	}
	if absent := q.Entries()[0]; !absent.SourceBlocked || absent.FailureCode != "unreadable_source" || absent.SourceRevision != current.SourceRevision {
		t.Fatalf("missing source outcome = %+v", absent)
	}
}

func TestPiAutomaticQueueLeafOnlyUpdatesAndRestart(t *testing.T) {
	home, path, data := piQueueSource(t)
	data = append(data, []byte(`{"type":"message","id":"skill-call","parentId":"u","timestamp":"2026-10-01T12:00:04Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"skill-read","name":"read","arguments":{"path":"skills/queue/SKILL.md"}}]}}`+"\n"+`{"type":"message","id":"skill-result","parentId":"skill-call","timestamp":"2026-10-01T12:00:05Z","message":{"role":"toolResult","toolCallId":"skill-read","content":"synthetic observed skill"}}`+"\n")...)
	grokQueueWrite(t, path, data)
	identity, err := pi.ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	leaf := "a"
	owner := pi.RuntimeRecord{Version: 1, RuntimeID: "synthetic", PID: os.Getpid(), ProcessStartIdentity: identity, StartedAtMs: 1, SessionID: "pi.custom_01", TranscriptPath: path, LeafID: &leaf, WorkState: "idle", Sequence: 1, UpdatedAtMs: 1}
	writeOwner := func() {
		data, err := json.Marshal(owner)
		if err != nil {
			t.Fatal(err)
		}
		grokQueueWrite(t, filepath.Join(home, "coslash", "pi-runtime", "synthetic.json"), data)
	}
	writeOwner()
	queueRoot, spool := t.TempDir(), t.TempDir()
	q, err := Open(queueRoot)
	if err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	hub := &grokQueueHub{planHub: &planHub{plan: plan}, records: map[string]fullsessionv1.Record{}}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	newRunner := func() *Runner {
		manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool})
		r := &Runner{Queue: q, Backup: manager, Hub: hub, Now: func() time.Time { return now }, RequireImportPlan: true,
			Discover: func(ctx context.Context) ([]*session.Session, error) { return nil, nil }}
		r.DiscoverBatches = func(ctx context.Context, emit func(DiscoveryBatch) error) error {
			sources, ok := pi.SourcePlanFromContext(ctx)
			if !ok {
				var err error
				sources, err = pi.PlanSourcesContext(ctx)
				if err != nil {
					return err
				}
			}
			for batch, err := range inventory.Discover(ctx, inventory.DiscoverOptions{Pi: sources, BatchFamilies: 1}) {
				if err != nil {
					return err
				}
				if err := emit(DiscoveryBatch{Sessions: batch.Sessions, PiInputs: batch.PiInputs, ContentBytes: batch.ContentBytes}); err != nil {
					return err
				}
			}
			return nil
		}
		return r
	}
	runner := newRunner()
	run := func() {
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
	if len(hub.requests) != 1 || hub.records["pi.custom_01"].Session.Summary == nil || *hub.records["pi.custom_01"].Session.Summary != "first branch" {
		t.Fatalf("first Pi upload = %+v, requests=%d", hub.records, len(hub.requests))
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	activity, revision := q.Entries()[0].Activity, q.Entries()[0].SourceRevision
	leaf, owner.Sequence = "b", 2
	writeOwner()
	run()
	if len(hub.requests) != 2 || *hub.records["pi.custom_01"].Session.Summary != "last branch" || q.Entries()[0].Activity != activity || q.Entries()[0].SourceRevision == revision {
		t.Fatalf("leaf-only upload = %+v, queue=%+v", hub.records, q.Entries())
	}
	after, err := os.Stat(path)
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("leaf-only fixture changed its transcript", err)
	}
	owner.Sequence, owner.UpdatedAtMs, owner.Entrypoint = 3, 900, "pi-rpc"
	writeOwner()
	run()
	q, err = Open(queueRoot)
	if err != nil {
		t.Fatal(err)
	}
	runner = newRunner()
	run()
	if len(hub.requests) != 2 {
		t.Fatalf("heartbeat/modality/restart duplicated upload: %d", len(hub.requests))
	}
	if skills := hub.records["pi.custom_01"].Session.Skills; len(skills) != 1 || skills[0].Name != "queue" || skills[0].Content != "synthetic observed skill" {
		t.Fatalf("observed Skills lost through capture/queue/restart: %+v", skills)
	}
}

func TestPiRevisionWorkHonorsPolicyAndOutOfWindowSources(t *testing.T) {
	for _, name := range []string{"local pause", "hub pause", "device off", "battery", "left out", "outside window"} {
		t.Run(name, func(t *testing.T) {
			_, path, valid := piQueueSource(t)
			grokQueueWrite(t, path, append(valid, []byte("{invalid}\n")...))
			sources, err := pi.PlanSourcesContext(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			q, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			config := hubclient.V4Config{ImportPlan: &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 30}}
			entry := Entry{Key: "pi", Selection: sessionbackupproducer.Selection{Agent: "pi", SessionID: "pi.custom_01"}, Session: hubclient.V4Session{Agent: "pi", Repo: "owner/repo"}, Activity: now.Add(-time.Hour).UnixMilli()}
			runner := &Runner{Queue: q, Now: func() time.Time { return now }, checkedAt: now}
			switch name {
			case "local pause":
				runner.LocalPause = func() bool { return true }
			case "hub pause":
				config.Paused = true
			case "device off":
				config.DeviceOff = true
			case "battery":
				runner.Conditions = func(context.Context) (bool, int, error) { return false, 1, nil }
			case "left out":
				config.LeaveOut = []string{"owner/repo"}
			case "outside window":
				entry.Activity = now.Add(-10 * 24 * time.Hour).UnixMilli()
			}
			runner.config = config
			if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: config}, now); err != nil {
				t.Fatal(err)
			}
			err = runner.syncPiSources(t.Context(), sources, []Entry{entry}, map[string]string{}, nil)
			if name == "left out" || name == "outside window" {
				if err != nil {
					t.Fatalf("ineligible source was parsed/frozen: %v", err)
				}
			} else if !errors.Is(err, ErrPaused) {
				t.Fatalf("policy did not stop revision work: %v", err)
			}
			if len(q.Entries()) != 0 {
				t.Fatalf("ineligible source entered queue: %+v", q.Entries())
			}
		})
	}
}

func TestPiPreparedRevisionMustMatchQueuedSnapshot(t *testing.T) {
	_, _, _ = piQueueSource(t)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	entry := Entry{Key: "pi", Selection: sessionbackupproducer.Selection{SourceKind: "local", SourceID: "local", Agent: "pi", SessionID: "pi.custom_01"}, Session: hubclient.V4Session{Agent: "pi"}, Activity: now.UnixMilli(), SourceRevision: "stale-revision"}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()})
	runner := &Runner{Queue: q, Backup: manager}
	err = runner.prepareEntry(t.Context(), &entry)
	var failure *sessionbackupproducer.PreparationError
	if !errors.As(err, &failure) || len(failure.Coverage.Problems) != 1 || failure.Coverage.Problems[0].Code != backup.ProblemUnstable || entry.Manifest != nil || runner.discoveryRequests.Load() == 0 {
		t.Fatalf("mismatched preparation = %+v, %v", entry, err)
	}
}

func TestPiBlockedSourceIsNotListedBeforeVerification(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
	config := hubclient.V4Config{ImportPlan: plan}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: config}, now); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "pi", Selection: sessionbackupproducer.Selection{Agent: "pi", SessionID: "root"}, Session: hubclient.V4Session{Agent: "pi", LocalKeyHash: "pi"}, Activity: now.UnixMilli(), SourceBlocked: true}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	hub := &planHub{plan: plan}
	runner := &Runner{Queue: q, Hub: hub, config: config, checkedAt: now, Now: func() time.Time { return now }}
	if err := runner.listAll(t.Context(), *plan); err != nil {
		t.Fatal(err)
	}
	if len(hub.lists) != 0 {
		t.Fatalf("unverified source listed: %+v", hub.lists)
	}
}
