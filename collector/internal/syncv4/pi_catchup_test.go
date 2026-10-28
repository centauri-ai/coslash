package syncv4

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

func TestPiFrozenCatchUpChecksPendingSourceBeforeResume(t *testing.T) {
	_, path, data := piQueueSource(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 1, MaxSessionsPerAgent: 1}
	config := hubclient.V4Config{ImportPlan: plan}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: config}, now); err != nil {
		t.Fatal(err)
	}
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()})
	selection := sessionbackupproducer.Selection{SourceKind: "local", SourceID: "local", Agent: "pi", SessionID: "pi.custom_01"}
	prepared, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	hub := &grokQueueHub{planHub: &planHub{plan: plan}, records: map[string]fullsessionv1.Record{}}
	runner := &Runner{Queue: q, Backup: manager, Hub: hub, config: config, checkedAt: now, Now: func() time.Time { return now }, Version: "test"}
	manifest, err := runner.manifest(prepared)
	if err != nil {
		t.Fatal(err)
	}
	key := localKey("local", "pi", selection.SessionID)
	meta := hubclient.V4Session{Agent: "pi", LocalKeyHash: key}
	status, err := hub.V4Create(t.Context(), hubclient.V4Create{Session: meta, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: key, Selection: selection, Session: meta, Activity: now.Add(-24 * time.Hour).UnixMilli(), SourceRevision: prepared.Manifest.Source.SourceRevision, BundleID: prepared.BundleID, Manifest: &manifest, ContentSHA256: manifest.ContentSHA256, UploadID: status.UploadID, SessionID: status.SessionID}
	if err := q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.FreezeCatchUp(*plan); err != nil {
		t.Fatal(err)
	}
	newerData := strings.Replace(string(data), `"id":"pi.custom_01"`, `"id":"newer-unselected"`, 1)
	grokQueueWrite(t, filepath.Join(filepath.Dir(path), "newer.jsonl"), []byte(newerData))
	newer := catchUpEntry(q, localKey("local", "pi", "newer-unselected"), "pi", now.Add(-time.Hour))
	newer.Selection = sessionbackupproducer.Selection{SourceKind: "local", SourceID: "local", Agent: "pi", SessionID: "newer-unselected"}
	if err := q.Merge([]Entry{newer}, now); err != nil {
		t.Fatal(err)
	}
	if selected := q.PlannedEntries(*plan, now); len(selected) != 1 || selected[0].Key != key {
		t.Fatalf("frozen fixture = %+v", selected)
	}
	grokQueueWrite(t, path, append(data, []byte("{invalid}\n")...))
	sources, err := pi.PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.syncPiSources(t.Context(), sources, nil, map[string]string{}, nil); err == nil {
		t.Error("malformed pending source escaped verification")
	}
	selected := q.PlannedEntries(*plan, now)
	if len(selected) != 1 || selected[0].Key != key {
		t.Fatalf("verification changed frozen membership: %+v", selected)
	}
	current := selected[0]
	if !current.SourceBlocked || current.BundleID != entry.BundleID || current.UploadID != entry.UploadID || current.SourceRevision != entry.SourceRevision || len(q.Discards()) != 0 {
		t.Errorf("last-good pending capture = %+v", current)
	}
	if err := runner.transfer(t.Context(), &current); !errors.Is(err, ErrPaused) {
		t.Errorf("blocked transfer = %v", err)
	}
	if len(hub.records) != 0 {
		t.Fatalf("malformed current source resumed old records: %+v", hub.records)
	}
}

func TestPiRevisionCandidatesPreserveFrozenAdmission(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		name := "first import"
		if frozen {
			name = "frozen with remaining capacity"
		}
		t.Run(name, func(t *testing.T) {
			q, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 2, MaxSessionsPerAgent: 2}
			if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan, LeaveOut: []string{"private"}}}, now); err != nil {
				t.Fatal(err)
			}
			entry := func(id string, age time.Duration) Entry {
				return Entry{Key: id, Selection: sessionbackupproducer.Selection{Agent: "pi", SessionID: id}, Session: hubclient.V4Session{Agent: "pi"}, Activity: now.Add(-age).UnixMilli()}
			}
			if err := q.Merge([]Entry{entry("selected", time.Hour)}, now); err != nil {
				t.Fatal(err)
			}
			if frozen {
				if err := q.FreezeCatchUp(*plan); err != nil {
					t.Fatal(err)
				}
			}
			before := q.Entries()
			excluded := entry("excluded", time.Minute)
			excluded.Session.Repo = "private"
			found := []Entry{entry("later", 2*time.Hour), entry("capped", 3*time.Hour), entry("outside", 10*24*time.Hour), excluded}
			selected := q.piRevisionCandidates(found)
			if len(selected) != 2 || selected[0].Key != "selected" || selected[1].Key != "later" {
				t.Fatalf("eligible catch-up rows = %+v", selected)
			}
			if !reflect.DeepEqual(before, q.Entries()) {
				t.Fatal("verification changed durable catch-up selection")
			}
		})
	}
}

func TestPiFrozenCandidatesKeepResumableRowsAlongsideNewActivity(t *testing.T) {
	for _, agent := range []string{"pi", "codex"} {
		t.Run(agent, func(t *testing.T) {
			q, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 1, MaxSessionsPerAgent: 1}
			if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
				t.Fatal(err)
			}
			if err := q.Merge([]Entry{catchUpEntry(q, "selected", "pi", now.Add(-time.Hour))}, now); err != nil {
				t.Fatal(err)
			}
			if err := q.FreezeCatchUp(plan); err != nil {
				t.Fatal(err)
			}
			if err := q.Merge([]Entry{catchUpEntry(q, "unselected", agent, now.Add(-time.Minute))}, now); err != nil {
				t.Fatal(err)
			}
			before := q.Entries()
			selected := q.piRevisionCandidates([]Entry{catchUpEntry(q, "live", "pi", now.Add(time.Second))})
			if keys := entryKeys(selected); !reflect.DeepEqual(keys, []string{"live", "selected"}) {
				t.Fatalf("verification scope = %v", keys)
			}
			if !reflect.DeepEqual(before, q.Entries()) || !reflect.DeepEqual(entryKeys(q.PlannedEntries(plan, now)), []string{"selected"}) {
				t.Fatal("verification changed the real frozen plan")
			}
		})
	}
}
