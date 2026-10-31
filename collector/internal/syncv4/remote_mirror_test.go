package syncv4

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func mirrorEntries(q *Queue, now time.Time) (Entry, Entry) {
	item := &session.Session{Agent: "claude", ID: fixtureRootID, LastActivityTime: now.UnixMilli()}
	local := discoveredEntries([]*session.Session{item}, q.InstallID(), nil, nil, nil)[0]
	ssh := RemoteEntries(remote.ListResult{Sessions: []remote.IndexedSession{{
		Key:     remote.SessionKey{SourceID: "r_0123456789abcdef", Agent: "claude", SourceSessionID: item.ID},
		Session: item, RevisionID: "verified-root", FamilyRevision: "remote-revision",
	}}}, q.InstallID(), "/remote")[0]
	ssh.Session.SSHHost, ssh.Session.SSHHostID = "fixture-box", "host-one"
	return local, ssh
}

func TestConfirmedMirrorReusesHistoricalHubIdentityAndSurvivesRestart(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	local, ssh := mirrorEntries(q, now)
	local.Activity = now.Add(time.Hour).UnixMilli()
	local.SessionID, local.RevisionID, local.BundleID = "ses_existing", "accepted-local-revision", "local-bundle"
	local.SyncedActivity, local.SyncedSourceRevision = local.Activity, local.SourceRevision
	if err := q.Merge([]Entry{local}, now); err != nil {
		t.Fatal(err)
	}
	// The old queue has no mirror flag; fresh native layout proof supplies it.
	ssh.LocalMirror = true
	if err := q.Merge([]Entry{ssh}, now); err != nil {
		t.Fatal(err)
	}
	entries := q.Entries()
	if len(entries) != 1 {
		t.Fatalf("mirror duplicated: %+v", entries)
	}
	got := entries[0]
	if got.Key != local.Key || got.Session.LocalKeyHash != local.Key || got.SessionID != "ses_existing" ||
		got.Selection != ssh.Selection || got.Activity != ssh.Activity || got.Session.SSHHost != "fixture-box" || got.RevisionID != "" || got.BundleID != "" || !pending(got) {
		t.Fatalf("mirror origin was not rebound in place: %+v", got)
	}
	if q.Matches(local) {
		t.Fatal("stale local capture may overwrite the verified SSH origin")
	}
	if err := q.MarkListedAt([]hubclient.V4ListResult{{LocalKeyHash: local.Key, SessionID: "ses_existing", State: "existing"}}, now); err != nil {
		t.Fatal(err)
	}
	if !pending(q.Entries()[0]) {
		t.Fatal("an accepted old local revision skipped verified SSH capture")
	}
	local.LocalMirror = true
	if err := q.Merge([]Entry{local}, now); err != nil {
		t.Fatal(err)
	}
	if got := q.Entries()[0]; got.Selection.SourceKind != backup.SourceSSH || got.Session.SSHHost != "fixture-box" {
		t.Fatalf("a later local discovery removed SSH attribution: %+v", got)
	}
	q, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ssh.LocalMirror = false // The Mac mirror can disappear after the adoption.
	if err := q.Merge([]Entry{ssh}, now); err != nil {
		t.Fatal(err)
	}
	if got := q.Entries(); len(got) != 1 || got[0].Key != local.Key || got[0].SessionID != "ses_existing" || !got[0].LocalMirror {
		t.Fatalf("restart changed canonical identity: %+v", got)
	}
	// A genuinely local session with the same vendor ID remains independent.
	local.LocalMirror = false
	local.SessionID, local.RevisionID, local.BundleID = "", "", ""
	if err := q.Merge([]Entry{local}, now); err != nil {
		t.Fatal(err)
	}
	if got := q.Entries(); len(got) != 2 || got[0].Key == got[1].Key || got[0].Selection.SourceKind == got[1].Selection.SourceKind {
		t.Fatalf("ordinary local session collided with the adopted mirror: %+v", got)
	}
}

func TestLateLocalMirrorKeepsExistingRemoteIdentity(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	local, ssh := mirrorEntries(q, now)
	if err := q.Merge([]Entry{ssh}, now); err != nil {
		t.Fatal(err)
	}
	local.LocalMirror = true
	if err := q.Merge([]Entry{local}, now); err != nil {
		t.Fatal(err)
	}
	ssh.LocalMirror = true
	if err := q.Merge([]Entry{ssh}, now); err != nil {
		t.Fatal(err)
	}
	if got := q.Entries(); len(got) != 1 || got[0].Key != ssh.Key || !got[0].LocalMirror {
		t.Fatalf("late mirror created a second identity: %+v", got)
	}
}

func TestMirrorAdoptionDoesNotMergeReplacedSSHHosts(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	_, ssh := mirrorEntries(q, now)
	ssh.LocalMirror = true
	if err := q.Merge([]Entry{ssh}, now); err != nil {
		t.Fatal(err)
	}
	ssh.Selection.SourceID = "r_fedcba9876543210"
	ssh.Key = localKey(ssh.Selection.SourceID, ssh.Selection.Agent, ssh.Selection.SessionID)
	ssh.Session.LocalKeyHash, ssh.Session.SSHHostID = ssh.Key, "host-two"
	if err := q.Merge([]Entry{ssh}, now); err != nil {
		t.Fatal(err)
	}
	if got := q.Entries(); len(got) != 2 || got[0].Selection.SourceID == got[1].Selection.SourceID || got[0].Key == got[1].Key {
		t.Fatalf("host replacement merged source identities: %+v", got)
	}
	if _, err := Open(filepath.Dir(q.path)); err != nil {
		t.Fatal(err)
	}
}

func TestNewSSHSourceImportsRecentHistoryAfterRelayCompleted(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	plan := hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 2, MaxSessionsPerAgent: 2}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	q.SetActiveSince(now.Add(-time.Hour))
	q.state.LiveOnlyPlanVersion, q.state.CatchUpFrozenVersion, q.state.Phase = 1, 1, "complete"
	entry := func(id string, age time.Duration) Entry {
		selection := sessionbackupproducer.Selection{SourceKind: backup.SourceSSH, SourceID: "r_0123456789abcdef", Agent: "codex", SessionID: id}
		key := localKey(selection.SourceID, selection.Agent, selection.SessionID)
		return Entry{Key: key, Selection: selection, SourceRevision: "verified-" + id, Activity: now.Add(-age).UnixMilli(),
			Session: hubclient.V4Session{Agent: "codex", LocalKeyHash: key, InstallID: q.InstallID(), Title: id}}
	}
	// Every root predates this Local process. The new source still gets one
	// bounded recent selection; the oldest and out-of-window roots stay local.
	if _, err := q.MergePlannedDiscovery([]Entry{entry("old", 96*time.Hour), entry("third", 60*time.Hour), entry("second", 48*time.Hour), entry("first", 24*time.Hour)}, now, plan, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := q.PlannedEntries(plan, now); len(got) != 2 || got[0].Selection.SessionID != "first" || got[1].Selection.SessionID != "second" {
		t.Fatalf("new-source selection ignored the window or caps: %+v", got)
	}
	q, err = Open(filepath.Dir(q.path))
	if err != nil {
		t.Fatal(err)
	}
	q.SetActiveSince(now.Add(time.Hour))
	if _, err := q.MergePlannedDiscovery([]Entry{entry("third", 60*time.Hour), entry("fourth", 30*time.Hour)}, now.Add(time.Hour), plan, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := q.Entries(); len(got) != 2 {
		t.Fatalf("restart reopened the source's initial quota: %+v", got)
	}
	if _, err := q.MergePlannedDiscovery([]Entry{entry("live", -2*time.Hour)}, now.Add(2*time.Hour), plan, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := q.PlannedEntries(plan, now.Add(2*time.Hour)); len(got) != 3 {
		t.Fatalf("initial quota blocked a later live session: %+v", got)
	}
}

func TestCompletedSSHBackfillDoesNotReopenDefaultCatchup(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	backfill := hubclient.V4ImportPlan{Version: 1, Window: "all", Backfill: true}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &backfill}}, now.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, ssh := mirrorEntries(q, now)
	ssh.Activity = now.Add(-2 * time.Hour).UnixMilli()
	if _, err := q.MergePlannedDiscovery([]Entry{ssh}, now.Add(-time.Hour), backfill, now.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	q.state.Phase = "complete"
	plan := hubclient.V4ImportPlan{Version: 2, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 2, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
		t.Fatal(err)
	}
	q.SetActiveSince(now)
	ssh.Selection.SessionID = "created-while-offline"
	ssh.Key = localKey(ssh.Selection.SourceID, ssh.Selection.Agent, ssh.Selection.SessionID)
	ssh.Session.LocalKeyHash = ssh.Key
	ssh.Activity = now.Add(-time.Hour).UnixMilli()
	if _, err := q.MergePlannedDiscovery([]Entry{ssh}, now, plan, now); err != nil {
		t.Fatal(err)
	}
	if got := q.PlannedEntries(plan, now); len(got) != 0 {
		t.Fatalf("completed backfill started another automatic import: %+v", got)
	}
}
