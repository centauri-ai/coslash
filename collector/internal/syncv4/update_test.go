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
	if err = q.ReconcileUpdate("1.2.4", []hubclient.V4Command{c}, sent, true); err != nil || q.PendingUpdate() == nil {
		t.Fatal("redelivery must retain attempt", err)
	}
	if err = q.ConfirmUpdateHandoff(id); err != nil {
		t.Fatal(err)
	}
	if err = q.ReconcileUpdate("1.2.4", nil, q.Results(), true); err != nil || q.PendingUpdate() != nil {
		t.Fatal("acknowledged completed attempt must clear", err)
	}
}

func TestReconcileUpdateRetainsUnconfirmedTargetAcrossRestart(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	const id = "10000000-0000-4000-8000-000000000001"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "1.2.4", "expiresAt": now.Add(time.Hour)})
	if err := q.AcceptUpdate(hubclient.V4Command{ID: id, Type: "update", Payload: payload}, "1.2.3", now); err != nil {
		t.Fatal(err)
	}
	sent := []hubclient.V4CommandResult{{CommandID: id, Result: "in_progress"}}
	if err := q.ReconcileUpdate("1.2.4", nil, q.Results(), true); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root)
	if err != nil || q.PendingUpdate() == nil {
		t.Fatalf("unconfirmed attempt lost: %v", err)
	}
	if err := q.ConfirmUpdateHandoff(id); err != nil {
		t.Fatal(err)
	}
	if err := q.ReconcileUpdate("1.2.4", nil, sent, true); err != nil {
		t.Fatal(err)
	}
	if q.PendingUpdate() == nil {
		t.Fatal("an earlier progress receipt cleared the confirmed attempt")
	}
	if err := q.ReconcileUpdate("1.2.4", nil, q.Results(), true); err != nil {
		t.Fatal(err)
	}
	if q.PendingUpdate() != nil {
		t.Fatal("confirmed attempt was not reconciled")
	}
}

func TestReconcileUpdateStillClearsFailedAndNewer(t *testing.T) {
	for _, tc := range []struct{ current, stage string }{{"1.2.3", "failed"}, {"1.2.5", "finalizing"}} {
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
		code := ""
		if tc.stage == "failed" {
			code = "update_failed"
		}
		if err := q.SetUpdateStage(id, tc.stage, code); err != nil {
			t.Fatal(err)
		}
		result := "in_progress"
		if tc.stage == "failed" {
			result = "failed"
		}
		if err := q.ReconcileUpdate(tc.current, nil, []hubclient.V4CommandResult{{CommandID: id, Result: result}}, true); err != nil {
			t.Fatal(err)
		}
		if q.PendingUpdate() != nil {
			t.Fatalf("%s attempt was not reconciled", tc.current)
		}
	}
}

func TestUpdateReadinessUsesBaselineDoneReceipt(t *testing.T) {
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
	if got := q.Results(); len(got) != 1 || got[0].Result != "in_progress" {
		t.Fatalf("unconfirmed: %+v", got)
	}
	if err := q.ConfirmUpdateHandoff(id); err != nil {
		t.Fatal(err)
	}
	if got := q.Results(); len(got) != 1 || got[0].Result != "done" || got[0].Progress != nil {
		t.Fatalf("confirmed: %+v", got)
	}
	if err := q.ReconcileUpdate("1.2.4", nil, q.Results(), false); err != nil || q.PendingUpdate() == nil {
		t.Fatalf("older Hub lost attempt: %v", err)
	}
	if err := q.ReconcileUpdate("1.2.4", nil, q.Results(), true); err != nil || q.PendingUpdate() != nil {
		t.Fatalf("update-aware Hub did not reconcile: %v", err)
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

func TestLegacySettlementIsBoundedNoDownloadWork(t *testing.T) {
	now := time.Now().UTC()
	const id = "10000000-0000-4000-8000-000000000001"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "1.2.4", "expiresAt": now.Add(-24 * time.Hour), "legacySettlement": true})
	command := hubclient.V4Command{ID: id, Type: "update", Payload: payload}
	for _, current := range []string{"1.2.4", "1.2.5"} {
		root := t.TempDir()
		q, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		if err = q.AcceptUpdate(command, current, now); err != nil {
			t.Fatalf("%s: %v", current, err)
		}
		if got := q.PendingUpdate(); got == nil || !got.LegacySettlement || got.Stage != "finalizing" {
			t.Fatalf("%s: %+v", current, got)
		}
		q, err = Open(root)
		if err != nil {
			t.Fatal(err)
		}
		if err = q.AcceptUpdate(command, current, now); err != nil {
			t.Fatalf("replay: %v", err)
		}
		if err = q.ConfirmUpdateHandoff(id); err != nil {
			t.Fatal(err)
		}
		if err = q.ReconcileUpdate(current, nil, q.Results(), false); err != nil || q.PendingUpdate() == nil {
			t.Fatalf("unaware acknowledgement: %v", err)
		}
		if err = q.ReconcileUpdate(current, nil, q.Results(), true); err != nil || q.PendingUpdate() != nil {
			t.Fatalf("aware acknowledgement: %v", err)
		}
	}
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = q.AcceptUpdate(command, "1.2.3", now); err == nil {
		t.Fatal("accepted legacy settlement before target was installed")
	}
}
