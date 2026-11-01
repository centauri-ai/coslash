package syncv4

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestUpdateAttemptSurvivesRestartAndRedelivery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	root := filepath.Join(t.TempDir(), "queue")
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	const id = "10000000-0000-4000-8000-000000000001"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "1.2.4", "expiresAt": now.Add(time.Minute)})
	c := hubclient.V4Command{ID: id, Type: "update", Payload: payload}
	if err = q.AcceptUpdate(c, "1.2.3", now); err != nil {
		t.Fatal(err)
	}
	if err = q.SetUpdateStage(id, "finalizing", ""); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.PendingUpdate(); got == nil || got.ID != id || got.Stage != "finalizing" {
		t.Fatalf("reopened: %+v", got)
	}
	if err = q.AcceptUpdate(c, "1.2.4", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("accepted replay after expiry: %v", err)
	}
	sent := q.Results()
	if len(sent) != 1 || sent[0].Result != "in_progress" {
		t.Fatalf("progress: %+v", sent)
	}
	if err = q.ReconcileUpdate("1.2.4", []hubclient.V4Command{c}, sent); err != nil || q.PendingUpdate() == nil {
		t.Fatal("redelivery must retain attempt", err)
	}
	if err = q.ReconcileUpdate("1.2.4", nil, sent); err != nil || q.PendingUpdate() != nil {
		t.Fatal("acknowledged completed attempt must clear", err)
	}
}

func TestUpdateReadinessIsAdvertisedOnlyAfterHubNegotiation(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	const id = "10000000-0000-4000-8000-000000000001"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "1.2.4", "expiresAt": now.Add(time.Hour)})
	if err := q.AcceptUpdate(hubclient.V4Command{ID: id, Type: "update", Payload: payload}, "1.2.3", now); err != nil {
		t.Fatal(err)
	}
	if err := q.ConfirmUpdateHandoff(id); err != nil {
		t.Fatal(err)
	}
	if got := q.Progress().UpdateReadyAttemptID; got != "" {
		t.Fatalf("sent readiness before negotiation: %q", got)
	}
	if err := q.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 1, Capabilities: []string{"update-completion/v1"}}); err != nil {
		t.Fatal(err)
	}
	if got := q.Progress().UpdateReadyAttemptID; got != id {
		t.Fatalf("readiness = %q", got)
	}
}

func TestUpdateRejectsInvalidExpiredAndConflictingWork(t *testing.T) {
	now := time.Now().UTC()
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const id = "10000000-0000-4000-8000-000000000001"
	makeCommand := func(target string, expiry time.Time) hubclient.V4Command {
		p, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": target, "expiresAt": expiry})
		return hubclient.V4Command{ID: id, Type: "update", Payload: p}
	}
	for _, target := range []string{"1.2.2", "1.2.3"} {
		if err := q.AcceptUpdate(makeCommand(target, now.Add(time.Hour)), "1.2.3", now); !errors.Is(err, ErrUpdateAlreadyCurrent) || q.PendingUpdate() != nil {
			t.Fatalf("already current %q: %v", target, err)
		}
	}
	preReleaseQueue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := preReleaseQueue.AcceptUpdate(makeCommand("1.2.4", now.Add(time.Hour)), "1.2.4-rc.1", now); err != nil || preReleaseQueue.PendingUpdate() == nil {
		t.Fatalf("stable upgrade from prerelease: %v", err)
	}
	for _, c := range []hubclient.V4Command{makeCommand("1.2.4-rc.1", now.Add(time.Hour)), makeCommand("1.2.4", now.Add(-time.Second)), {ID: id, Payload: []byte(`{"attemptId":"10000000-0000-4000-8000-000000000001","targetVersion":"1.2.4","expiresAt":"2099-01-01T00:00:00Z","url":"https://example.com"}`)}} {
		if err := q.AcceptUpdate(c, "1.2.3", now); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	c := makeCommand("1.2.4", now.Add(time.Hour))
	if err = q.AcceptUpdate(c, "1.2.3", now); err != nil {
		t.Fatal(err)
	}
	if err = q.AcceptUpdate(makeCommand("1.2.5", now.Add(time.Hour)), "1.2.3", now); err == nil {
		t.Fatal("accepted conflicting replay")
	}
}
