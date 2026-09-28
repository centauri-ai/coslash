package syncv4

import (
	"os"
	"path/filepath"
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
