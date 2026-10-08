package syncv4

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
)

func TestQueuePersistsInstallIdentityAndRecentFirstProgress(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	queue, err := Open(filepath.Join(root, "sync"))
	if err != nil {
		t.Fatal(err)
	}
	installID := queue.InstallID()
	if len(installID) != 36 {
		t.Fatalf("install ID = %q; want UUID", installID)
	}

	recent := Entry{Key: "recent", Activity: now.Add(-time.Hour).UnixMilli(), Session: hubclient.V4Session{Agent: "codex"},
		Selection: sessionbackupproducer.Selection{Agent: "codex", SessionID: "recent"}}
	older := Entry{Key: "older", Activity: now.Add(-90 * 24 * time.Hour).UnixMilli(), Session: hubclient.V4Session{Agent: "codex"},
		Selection: sessionbackupproducer.Selection{Agent: "codex", SessionID: "older"}}
	if err := queue.Merge([]Entry{older, recent}, now); err != nil {
		t.Fatal(err)
	}
	entries := queue.Entries()
	if len(entries) != 2 || entries[0].Key != "recent" || !entries[0].Recent || entries[1].Recent {
		t.Fatalf("recent-first queue order = %+v", entries)
	}
	progress := queue.Progress()
	if progress.Pending != 2 || progress.FirstSync.RecentDone != 0 || progress.FirstSync.RecentTotal != 1 || progress.FirstSync.HistoryState != "syncing" {
		t.Fatalf("first-sync progress = %+v", progress)
	}

	reopened, err := Open(filepath.Join(root, "sync"))
	if err != nil {
		t.Fatal(err)
	}
	if reopened.InstallID() != installID {
		t.Fatalf("reopened install ID = %q; want %q", reopened.InstallID(), installID)
	}
}

func TestCWDLabelIsPathSafeAndEnforcesFolderLeaveOut(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	label := cwdLabel(filepath.Join(home, "private", "repo"))
	if label != "~/private/repo" {
		t.Fatalf("cwd label = %q; want home-relative path", label)
	}
	if leftOut(hubclient.V4Session{CWDLabel: label}, []string{"~/private"}) != true {
		t.Fatal("folder leave-out did not match a child directory")
	}
	if leftOut(hubclient.V4Session{CWDLabel: "~/private-other/repo"}, []string{"~/private"}) {
		t.Fatal("folder leave-out matched across a path boundary")
	}
}

func TestRepositoryLeaveOutWildcardMatchesOneRepository(t *testing.T) {
	if !leftOut(hubclient.V4Session{Repo: "acme/private"}, []string{"acme/*"}) {
		t.Fatal("repository wildcard did not match owner repository")
	}
	if leftOut(hubclient.V4Session{Repo: "acme/team/private"}, []string{"acme/*"}) {
		t.Fatal("repository wildcard matched a nested path")
	}
}

func TestQueueSessionStatesDistinguishHubAndPendingSessions(t *testing.T) {
	selectSession := func(id string) sessionbackupproducer.Selection {
		return sessionbackupproducer.Selection{SourceID: "local", Agent: "codex", SessionID: id}
	}
	queue := &Queue{state: state{Entries: []Entry{
		{Key: "in-hub", Selection: selectSession("in-hub"), Activity: 4, SyncedActivity: 4, RevisionID: "revision"},
		{Key: "syncing", Selection: selectSession("syncing"), Activity: 5, BundleID: "bundle", UploadID: "upload"},
		{Key: "not-in-hub", Selection: selectSession("not-in-hub"), Activity: 6},
		{Key: "left-out", Selection: selectSession("left-out"), Activity: 7, Excluded: true},
	}}}
	want := map[string]string{
		"local:codex:in-hub":     "in_hub",
		"local:codex:syncing":    "syncing",
		"local:codex:not-in-hub": "not_in_hub",
		"local:codex:left-out":   "left_out",
	}
	if got := queue.SessionStates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("session states=%v, want %v", got, want)
	}
}

func TestQueueRejectsStaleCompletionAfterNewActivity(t *testing.T) {
	now := time.Now().UTC()
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "codex-session", Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	entry.Activity++
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	entry.SyncedActivity = entry.Activity - 1
	if err := queue.Update(entry); err != nil {
		t.Fatal(err)
	}
	entry.Activity--
	if err := queue.Update(entry); err == nil {
		t.Fatal("stale completion update succeeded")
	}
	got := queue.Entries()[0]
	if got.Activity <= got.SyncedActivity {
		t.Fatalf("new activity was marked synced: %+v", got)
	}
}

func TestQueueSameActivityChangedContentNeedsNewUpload(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "codex-session", Activity: now.UnixMilli(), SourceRevision: "first", SyncedSourceRevision: "first",
		RevisionID: "rev_first", Session: hubclient.V4Session{Agent: "codex"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	stale := queue.Entries()[0]
	entry.SourceRevision = "second"
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if !pending(queue.Entries()[0]) {
		t.Fatal("new content with same activity was skipped")
	}
	stale.RevisionID = "rev_stale"
	if err := queue.Update(stale); err == nil {
		t.Fatal("old completion replaced a changed source")
	}
}

func TestRePairKeepsInstallIDAndReconcilesUploadState(t *testing.T) {
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installID := queue.InstallID()
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if err := queue.Rebind(first); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "codex", Activity: 100, SyncedActivity: 100, RevisionID: "rev_old", SessionID: "ses_old", BundleID: "old_bundle", Session: hubclient.V4Session{Agent: "codex"}}
	if err := queue.Merge([]Entry{entry}, time.UnixMilli(100)); err != nil {
		t.Fatal(err)
	}
	if err := queue.Rebind(second); err != nil {
		t.Fatal(err)
	}
	got := queue.Entries()[0]
	if queue.InstallID() != installID || !pending(got) || got.RevisionID != "" || got.SessionID != "" || got.BundleID != "" {
		t.Fatalf("re-pair state=%+v install=%q", got, queue.InstallID())
	}
}

func TestQueuePromotesRecentlyChangedHistoryIntoRecentBatch(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "old-codex-session", Activity: now.Add(-90 * 24 * time.Hour).UnixMilli(),
		Session: hubclient.V4Session{Agent: "codex"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	entry.Activity = now.Add(-time.Hour).UnixMilli()
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	got := queue.Entries()[0]
	if !got.Recent || got.Activity != entry.Activity {
		t.Fatalf("updated history entry was not promoted: %+v", got)
	}
}

// The owner's version-0 default policy is the Hub's policy too: Local applies
// it on first check-in, keeps it until a newer version, and forgets it when
// the Hub binding changes.
func TestQueueAppliesTheHubsFirstPolicyEvenAtVersionZero(t *testing.T) {
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := func(version int64, paused bool) hubclient.V4CheckIn {
		return hubclient.V4CheckIn{ConfigVersion: version, MinVersion: "0.0.3",
			Config: hubclient.V4Config{Paused: paused, LeaveOut: []string{}}}
	}
	if err := queue.ApplyPolicy(policy(0, true)); err != nil {
		t.Fatal(err)
	}
	if version, config, _ := queue.Policy(); version != 0 || !config.Paused {
		t.Fatalf("first policy not applied: version=%d config=%+v", version, config)
	}
	if err := queue.ApplyPolicy(policy(0, false)); err != nil {
		t.Fatal(err)
	}
	if _, config, _ := queue.Policy(); !config.Paused {
		t.Fatal("a repeated version replaced the known policy")
	}
	if err := queue.ApplyPolicy(policy(1, false)); err != nil {
		t.Fatal(err)
	}
	if version, config, _ := queue.Policy(); version != 1 || config.Paused {
		t.Fatalf("newer policy not applied: version=%d config=%+v", version, config)
	}
	if err := queue.Rebind(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := queue.Rebind(strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyPolicy(policy(0, true)); err != nil {
		t.Fatal(err)
	}
	if version, config, _ := queue.Policy(); version != 0 || !config.Paused {
		t.Fatalf("a new binding kept the old policy: version=%d config=%+v", version, config)
	}
}

func TestQueueAgentsAreTheQueuedSessionsAgents(t *testing.T) {
	now := time.Now().UTC()
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := queue.Agents(); len(got) != 0 {
		t.Fatalf("empty queue agents=%v", got)
	}
	var entries []Entry
	for i, agent := range []string{"cursor", "codex", "claude", "codex"} {
		entries = append(entries, Entry{Key: fmt.Sprintf("%s-%d", agent, i), Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: agent}})
	}
	if err := queue.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(queue.Agents(), ","); got != "claude,codex,cursor" {
		t.Fatalf("agents=%s", got)
	}
}

// Sessions waiting for the Hub's active-upload limit are pending, not
// failing; the device page would otherwise show a first sync as failures.
func TestProgressDoesNotCountBackPressureAsFailing(t *testing.T) {
	now := time.Now().UTC()
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	for i := range 3 {
		entries = append(entries, Entry{Key: fmt.Sprintf("codex-%d", i), Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}})
	}
	if err := queue.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	for i, code := range []string{"rate_limited", "unreadable_source", ""} {
		entry := queue.Entries()[i]
		entry.FailureCode = code
		if err := queue.Update(entry); err != nil {
			t.Fatal(err)
		}
	}
	if progress := queue.Progress(); progress.Pending != 3 || progress.Failing != 1 {
		t.Fatalf("progress=%+v", progress)
	}
}

// A prepared bundle that no entry refers to any more is scheduled for
// deletion whichever path dropped it: a new bundle, changed source activity,
// or a new Hub binding.
func TestQueueSchedulesAbandonedBundlesForDeletion(t *testing.T) {
	now := time.Now().UTC()
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{Key: "a", Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}},
		{Key: "b", Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}},
	}
	if err := queue.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	byKey := func(key string) Entry {
		for _, entry := range queue.Entries() {
			if entry.Key == key {
				return entry
			}
		}
		t.Fatalf("entry %s missing", key)
		return Entry{}
	}
	a := byKey("a")
	a.BundleID = strings.Repeat("1", 64)
	if err := queue.Update(a); err != nil {
		t.Fatal(err)
	}
	a.BundleID = strings.Repeat("2", 64)
	if err := queue.Update(a); err != nil {
		t.Fatal(err)
	}
	b := byKey("b")
	b.BundleID, b.ParkedVersion, b.FailureCode = strings.Repeat("3", 64), "0.0.5", "unreadable_source"
	if err := queue.Update(b); err != nil {
		t.Fatal(err)
	}
	changed := entries[1]
	changed.Activity = now.Add(time.Minute).UnixMilli()
	if err := queue.Merge([]Entry{changed}, now); err != nil {
		t.Fatal(err)
	}
	if got := byKey("b"); got.ParkedVersion != "" || got.FailureCode != "" || got.BundleID != "" {
		t.Fatalf("changed source stayed parked: %+v", got)
	}
	if err := queue.Rebind(strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if err := queue.Rebind(strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	want := []string{strings.Repeat("1", 64), strings.Repeat("3", 64), strings.Repeat("2", 64)}
	if got := queue.Discards(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("discards=%v", got)
	}
	if err := queue.ForgetDiscards(want[:2]); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filepath.Dir(queue.path))
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Discards(); len(got) != 1 || got[0] != want[2] {
		t.Fatalf("persisted discards=%v", got)
	}
}
