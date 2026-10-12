package syncv4

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestReadHealthReportsQueueFailuresWithoutChangingQueueFile(t *testing.T) {
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	entry := Entry{Key: "synthetic-key", Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex", Title: "synthetic private title"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	entry = queue.Entries()[0]
	entry.FailureCode, entry.ParkedVersion = "malformed_artifact", "0.2.0"
	if err := queue.Update(entry); err != nil {
		t.Fatal(err)
	}
	if err := queue.RecordCheckIn(now.Add(-time.Minute), "hub_unreachable"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "queue.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	health, err := ReadHealth(root)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ReadHealth changed the queue file")
	}
	if health.InstallID != queue.InstallID() || health.PolicyKnown || health.Pending != 1 || health.Failing != 1 || health.Parked != 1 || health.LastCheckInResult != "hub_unreachable" || health.LastCheckInAt == nil || !health.LastCheckInAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("health = %+v", health)
	}
	if len(health.TopFailureCodes) != 1 || health.TopFailureCodes[0] != (FailureCodeCount{Code: "malformed_artifact", Count: 1}) {
		t.Fatalf("top failure codes = %+v", health.TopFailureCodes)
	}
	encoded, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("synthetic private title")) {
		t.Fatal("health reported session title")
	}
}

func TestHubContactFailureStreakPersistsChangesAndResetsOnSuccess(t *testing.T) {
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})
	start := time.Date(2026, 10, 12, 8, 12, 0, 0, time.UTC)
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	if changed, err := queue.RecordHubContact(start, refused); err != nil || !changed {
		t.Fatalf("first failure changed=%t err=%v", changed, err)
	}
	if changed, err := queue.RecordHubContact(start.Add(time.Minute), refused); err != nil || changed {
		t.Fatalf("repeated failure changed=%t err=%v", changed, err)
	}
	failure := queue.LastHubFailure()
	if failure == nil || failure.Cause != hubclient.FailureConnect || failure.SinceMs != start.UnixMilli() || failure.Attempts != 2 {
		t.Fatalf("failure streak = %+v", failure)
	}

	unavailable := hubclient.V4Problem{Code: "maintenance", HTTPStatus: 503}
	if changed, err := queue.RecordHubContact(start.Add(2*time.Minute), unavailable); err != nil || !changed {
		t.Fatalf("changed cause changed=%t err=%v", changed, err)
	}
	if changed, err := queue.RecordHubContact(start.Add(3*time.Minute), unavailable); err != nil || changed {
		t.Fatalf("repeated changed cause changed=%t err=%v", changed, err)
	}
	failure = queue.LastHubFailure()
	if failure == nil || failure.Cause != hubclient.FailureHubUnavailable || failure.SinceMs != start.Add(2*time.Minute).UnixMilli() || failure.Attempts != 2 {
		t.Fatalf("changed-cause streak = %+v", failure)
	}
	health, err := ReadHealth(root)
	if err != nil || health.LastHubFailure == nil || *health.LastHubFailure != *failure {
		t.Fatalf("persisted failure = %+v, err=%v", health.LastHubFailure, err)
	}
	if changed, err := queue.RecordHubContact(start.Add(4*time.Minute), nil); err != nil || !changed {
		t.Fatalf("success reset changed=%t err=%v", changed, err)
	}
	if queue.LastHubFailure() != nil {
		t.Fatalf("success did not clear failure streak: %+v", queue.LastHubFailure())
	}
	wantLogs := "Hub contact failed: cause=connect\nHub contact failed: cause=hub_unavailable\nHub contact recovered: cause=ok\n"
	if output.String() != wantLogs {
		t.Fatalf("transition logs = %q, want %q", output.String(), wantLogs)
	}
}
