package syncv4

import (
	"context"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func TestSSHHostAdmissionRequiresCapabilityAndCurrentHost(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q.state.SSHHosts = []hubclient.V4SSHHost{{ID: "current-host"}}
	if q.SSHHostAllowed("current-host") {
		t.Fatal("host admitted without relay capability")
	}
	q.state.HubCapabilities = []string{hubclient.CapabilitySSHRelaySync}
	if !q.SSHHostAllowed("current-host") || q.SSHHostAllowed("replaced-host") {
		t.Fatal("host admission did not follow the current Hub binding")
	}
	q.state.SSHHosts = nil
	if q.SSHHostAllowed("current-host") {
		t.Fatal("removed host admitted")
	}
}

func TestRemoteDiscoveryIdentityRevisionAndAvailability(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := &session.Session{Agent: "claude", ID: "same-id", LastActivityTime: now.UnixMilli(), WorkingDirectory: "/home/fixture/private/project"}
	host := remote.IndexedSession{Key: remote.SessionKey{SourceID: "r_0123456789abcdef", Agent: item.Agent, SourceSessionID: item.ID}, Session: item, RevisionID: "root-revision", FamilyRevision: "family-one"}
	found := RemoteEntries(remote.ListResult{Sessions: []remote.IndexedSession{host}}, q.InstallID(), "/home/fixture")
	if found[0].Session.CWDLabel != "~/private/project" || !leftOut(found[0].Session, []string{"~/private"}) {
		t.Fatal("remote home-relative leave-out was lost")
	}
	local := discoveredEntries([]*session.Session{item}, q.InstallID(), nil, nil, nil)
	if len(found) != 1 || found[0].Key == local[0].Key || found[0].Session.LocalKeyHash != found[0].Key || found[0].Selection.SourceKind != sessionbackupv1.SourceSSH {
		t.Fatalf("source identity: %+v", found)
	}
	if err := q.Merge(append(local, found...), now); err != nil {
		t.Fatal(err)
	}
	if got := q.Agents(); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("local agents: %v", got)
	}
	for _, invalid := range []remote.IndexedSession{{Session: item}, {Key: host.Key, Session: item, RevisionID: host.RevisionID, FamilyRevision: host.FamilyRevision, DisplayStale: true}} {
		if got := RemoteEntries(remote.ListResult{Sessions: []remote.IndexedSession{invalid}}, q.InstallID(), "/home/fixture"); len(got) != 0 {
			t.Fatalf("admitted stale/incomplete card: %+v", got)
		}
	}
	available := false
	runner := &Runner{Queue: q, SourceAvailable: func(s sessionbackupproducer.Selection) bool {
		return s.SourceKind != sessionbackupv1.SourceSSH || available
	}}
	if err := runner.applyExclusions(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range q.Entries() {
		if entry.Excluded != (entry.Selection.SourceKind == sessionbackupv1.SourceSSH) {
			t.Fatalf("availability: %+v", entry)
		}
	}
	available = true
	if err := runner.applyExclusions(); err != nil {
		t.Fatal(err)
	}
	first := found[0]
	first.RevisionID = "accepted"
	first.SyncedActivity = first.Activity
	first.SyncedSourceRevision = first.SourceRevision
	if err := q.Update(first); err != nil {
		t.Fatal(err)
	}
	host.FamilyRevision = "family-two"
	if err := q.Merge(RemoteEntries(remote.ListResult{Sessions: []remote.IndexedSession{host}}, q.InstallID(), "/home/fixture"), now); err != nil {
		t.Fatal(err)
	}
	for _, entry := range q.Entries() {
		if entry.Key == first.Key && (entry.RevisionID != "" || entry.SourceRevision != "family-two") {
			t.Fatalf("child-only change missed: %+v", entry)
		}
	}
	reopened, err := Open(q.path[:len(q.path)-len("/queue.json")])
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Entries()) != 2 || reopened.InstallID() != q.InstallID() {
		t.Fatal("restart lost source identity")
	}
}

func TestRemoteDiscoveryRunsBeforeLocalBatchesAndHonorsPolicy(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "on", true: "paused"}[paused], func(t *testing.T) {
			now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
			q, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			hub := &planHub{plan: &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30}}
			remoteCalled, localCalled := false, false
			runner := &Runner{Queue: q, Hub: hub, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}), Now: func() time.Time { return now }, LocalPause: func() bool { return paused },
				Discover:       func(context.Context) ([]*session.Session, error) { return nil, nil },
				DiscoverRemote: func(context.Context, int64) ([]Entry, error) { remoteCalled = true; return nil, nil },
				DiscoverBatches: func(context.Context, func(DiscoveryBatch) error) error {
					if !remoteCalled {
						t.Error("remote discovery omitted before local stream")
					}
					localCalled = true
					return nil
				},
			}
			_ = runner.SyncOnce(t.Context())
			if !paused && (!remoteCalled || !localCalled) {
				t.Fatal("discovery not connected")
			}
			if paused && (remoteCalled || localCalled) {
				t.Fatal("pause did not stop discovery")
			}
		})
	}
}
