package syncv4

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type authorityHub struct {
	*planHub
	response hubclient.V4CheckIn
}

func (h *authorityHub) V4CheckIn(ctx context.Context, queue hubclient.V4Queue, version int64, results []hubclient.V4CommandResult, agents []string, logs []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	_, err := h.planHub.V4CheckIn(ctx, queue, version, results, agents, logs)
	return h.response, err
}

func TestSharedAuthorityCommandsAndRestart(t *testing.T) {
	now := time.Unix(1790000000, 0)
	id := "11111111-2222-4333-8444-555555555555"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "1.2.4", "expiresAt": now.Add(time.Hour)})
	for _, secondary := range []bool{false, true} {
		t.Run(map[bool]string{false: "primary", true: "secondary"}[secondary], func(t *testing.T) {
			root := t.TempDir()
			q, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			h := &authorityHub{planHub: &planHub{withoutScale: true}, response: hubclient.V4CheckIn{ConfigVersion: 1, SSHHosts: []hubclient.V4SSHHost{}, RecommendedUpdate: true, RecommendedVersion: "1.2.4", Commands: []hubclient.V4Command{{ID: "test", Type: "ssh.test"}, {ID: "install", Type: "ssh.install"}, {ID: id, Type: "update", Payload: payload}}}}
			calls, reconciles, cleanups := 0, 0, 0
			r := Runner{Version: "1.2.3", Queue: q, Hub: h, NoSharedAuthority: secondary, Now: func() time.Time { return now }, Command: func(context.Context, hubclient.V4Command) error { calls++; return nil }, ReconcileSSHHosts: func([]hubclient.V4SSHHost) error { reconciles++; return nil }, CleanupRemoteCache: func() { cleanups++ }}
			if err := r.refreshConsent(t.Context()); err != nil {
				t.Fatal(err)
			}
			if secondary {
				if calls != 0 || reconciles != 0 || cleanups != 0 || q.PendingUpdate() != nil || q.UpdatePrompt().Available {
					t.Fatal("secondary changed shared state")
				}
				q, err = Open(root)
				if err != nil {
					t.Fatal(err)
				}
				r.Queue = q
				results := q.Results()
				if len(results) != 3 {
					t.Fatalf("persisted results: %+v", results)
				}
				for _, result := range results {
					if result.Result != "failed" || (result.Error != "command_unavailable" && result.Error != "update_unavailable") {
						t.Fatalf("result: %+v", result)
					}
				}
				h.checkInErr = errors.New("offline")
				if err := r.refreshConsent(t.Context()); err == nil || len(q.Results()) != 3 {
					t.Fatal("failed check-in lost pending results")
				}
				h.checkInErr = nil
				if err := r.refreshConsent(t.Context()); err != nil {
					t.Fatal(err)
				}
				if len(h.results) != 3 || len(q.Results()) != 0 || calls != 0 {
					t.Fatalf("acknowledgement or deduplication failed: sent=%+v remaining=%+v", h.results, q.Results())
				}
			} else if calls != 2 || reconciles != 1 || cleanups != 1 || q.PendingUpdate() == nil || !q.UpdatePrompt().Available {
				t.Fatalf("primary behavior: calls=%d reconciles=%d cleanups=%d update=%+v prompt=%+v", calls, reconciles, cleanups, q.PendingUpdate(), q.UpdatePrompt())
			}
		})
	}
}

func TestSecondaryRejectsStaleUpdateBeforeCheckIn(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1790000000, 0)
	id := "11111111-2222-4333-8444-555555555555"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "1.2.4", "expiresAt": now.Add(time.Hour)})
	if err := q.AcceptUpdate(hubclient.V4Command{ID: id, Type: "update", Payload: payload}, "1.2.3", now); err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 1, RecommendedUpdate: true, RecommendedVersion: "1.2.4"}); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	h := &authorityHub{planHub: &planHub{withoutScale: true}, response: hubclient.V4CheckIn{ConfigVersion: 1}}
	r := Runner{Queue: q, Hub: h, NoSharedAuthority: true, Now: func() time.Time { return now }}
	if err := r.refreshConsent(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(h.results) != 1 || h.results[0].Result != "failed" || h.results[0].Error != "update_unavailable" || q.UpdatePrompt().Available {
		t.Fatalf("stale update: %+v", h.results)
	}
	if err := r.ReportUpdate(t.Context()); err == nil {
		t.Fatal("secondary reported update receipt")
	}
}
