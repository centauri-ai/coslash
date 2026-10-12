package syncv4

import (
	"bytes"
	"encoding/json"
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
