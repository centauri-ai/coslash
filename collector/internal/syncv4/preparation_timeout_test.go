package syncv4

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

type preparationRecoveryHub struct{ planHub }

func (h *preparationRecoveryHub) V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error) {
	h.creates++
	return hubclient.V4Status{SessionID: "ses_timeout", State: "completed", RevisionID: "recovered"}, nil
}

func TestPreparationBudgetsRecoverIdenticalInputWithoutManualRetry(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, phase := range []string{"warm-estimate", "warm-create", "planned", "planned-pass"} {
		t.Run(phase, func(t *testing.T) {
			home := t.TempDir()
			workspace := filepath.Join(home, "project")
			dir := filepath.Join(claude.ProjectsRoot(home), "project")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			row, err := json.Marshal(map[string]any{"sessionId": fixtureRootID, "type": "user", "timestamp": "2026-09-28T11:00:00Z", "cwd": workspace, "message": map[string]string{"content": "hello"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, fixtureRootID+".jsonl"), append(row, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			var expirePass context.CancelFunc
			manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir(), OpenSource: func(ctx context.Context, _ sessionbackupproducer.Selection) (sessionbackupproducer.SourceHandle, error) {
				if expirePass != nil {
					expirePass()
					expirePass = nil
					return sessionbackupproducer.SourceHandle{}, ctx.Err()
				}
				return sessionbackupproducer.SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
			}})
			queueRoot := t.TempDir()
			queue, err := Open(queueRoot)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
			plan := hubclient.V4ImportPlan{Version: 1, Window: "all", History: true}
			if err := queue.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: &plan}}, now); err != nil {
				t.Fatal(err)
			}
			queue.SetActiveSince(now)
			entry := Entry{Key: "timeout", SessionID: "ses_timeout", Listed: true, Activity: now.UnixMilli(), SourceRevision: "unchanged",
				Selection: sessionbackupproducer.Selection{SourceKind: "local", SourceID: "local", Agent: "claude", SessionID: fixtureRootID},
				Session:   hubclient.V4Session{InstallID: queue.InstallID(), Agent: "claude", LocalKeyHash: "timeout"}}
			if phase == "warm-create" {
				entry.ContentBytes = 100
			}
			if err := queue.Merge([]Entry{entry}, now); err != nil {
				t.Fatal(err)
			}
			hub := &preparationRecoveryHub{planHub: planHub{plan: &plan}}
			runner := &Runner{Queue: queue, Backup: manager, Hub: hub, Version: "0.3.1", checkedAt: now,
				Now: func() time.Time { return now }, config: hubclient.V4Config{ImportPlan: &plan}}
			expired := false
			runner.timeout = func(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
				if phase == "planned-pass" {
					ctx, cancel := context.WithCancel(ctx)
					if budget == plannedContentBudget {
						expirePass, expired = cancel, true
					}
					return ctx, cancel
				}
				if !expired && (phase != "planned" || budget == sourcePreparationBudget) {
					expired = true
					return context.WithDeadline(ctx, time.Time{})
				}
				return context.WithCancel(ctx)
			}
			run := func() error {
				if phase == "planned" || phase == "planned-pass" {
					return runner.syncPlannedContent(t.Context(), plan)
				}
				return runner.warmStart(t.Context(), plan, now, 30*time.Second, 0)
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			failed := queue.Entries()[0]
			if !expired || failed.ParkedVersion != "" || failed.RevisionID != "" || hub.creates != 0 {
				t.Fatalf("budget did not yield pending input: expired=%t entry=%+v creates=%d", expired, failed, hub.creates)
			}
			logs, _ := queue.PendingLog(now)
			if phase == "planned" {
				if failed.FailureCode != "unreadable_source" || !failed.RetryAt.Equal(now.Add(time.Minute)) || len(logs) != 1 || logs[0].Code != "unreadable_source" || logs[0].Parked {
					t.Fatalf("timeout retry/log = %+v / %+v", failed, logs)
				}
				if err := run(); err != nil {
					t.Fatal(err)
				}
				if hub.creates != 0 {
					t.Fatal("retried before backoff elapsed")
				}
				now = failed.RetryAt
				runner.checkedAt = now
			} else if failed.FailureCode != "" || len(logs) != 0 || !failed.RetryAt.IsZero() || failed.BackoffAttempt != 0 {
				t.Fatalf("pass budget reported bad data: %+v / %+v", failed, logs)
			}
			// Reopen durable queue state; no retry command or source mutation.
			runner.Queue, err = Open(queueRoot)
			if err != nil {
				t.Fatal(err)
			}
			runner.timeout = nil
			if err := run(); err != nil {
				t.Fatal(err)
			}
			recovered := runner.Queue.Entries()[0]
			if recovered.RevisionID != "recovered" || recovered.FailureCode != "" || recovered.ParkedVersion != "" || hub.creates != 1 {
				t.Fatalf("identical input did not recover automatically: %+v, creates=%d", recovered, hub.creates)
			}
		})
	}
}
