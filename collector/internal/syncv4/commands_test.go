package syncv4

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/remote"
)

func TestCommandRedeliveryAndMonotonicPolicySurviveRestart(t *testing.T) {
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "one", SessionID: "ses_one", Session: hubclient.V4Session{Agent: "codex", Repo: "owner/repo"}}
	if err := queue.Merge([]Entry{entry}, time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	checkins, launches := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkins++
		var input struct {
			AppliedConfigVersion int64                       `json:"appliedConfigVersion"`
			Results              []hubclient.V4CommandResult `json:"results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if checkins == 2 && (input.AppliedConfigVersion != 8 || len(input.Results) != 1 || input.Results[0].Result != "done") {
			t.Errorf("second check-in = %+v", input)
		}
		if checkins == 3 && (input.AppliedConfigVersion != 8 || len(input.Results) != 0) {
			t.Errorf("restart check-in = %+v", input)
		}
		if checkins == 1 {
			io.WriteString(w, `{"configVersion":8,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[{"id":"11111111-2222-3333-4444-555555555555","type":"launch","payload":{"sessionId":"ses_one","mode":"resume"}}],"minVersion":"0.0.3"}`)
		} else {
			io.WriteString(w, `{"configVersion":7,"config":{"paused":false,"deviceOff":false,"leaveOut":["owner/repo"],"agentKnowledge":true},"commands":[{"id":"11111111-2222-3333-4444-555555555555","type":"launch","payload":{"sessionId":"ses_one","mode":"resume"}}],"minVersion":"0.0.3"}`)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "v0.0.5"}
	run := func(q *Queue) {
		runner := &Runner{Queue: q, Hub: client, Command: func(context.Context, hubclient.V4Command) error { launches++; return nil }}
		if err := runner.refreshConsent(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	run(queue)
	run(queue)
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	run(reopened)
	version, config, _ := reopened.Policy()
	if launches != 1 || version != 8 || len(config.LeaveOut) != 0 {
		t.Fatalf("launches=%d policy=%d %+v", launches, version, config)
	}
}

func TestLeaveOutAndLocalPauseDenyRetryAndLaunch(t *testing.T) {
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Merge([]Entry{{Key: "one", SessionID: "ses_one", Session: hubclient.V4Session{Agent: "codex", Repo: "owner/repo"}}}, time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"configVersion":2,"config":{"paused":false,"deviceOff":false,"leaveOut":["owner/repo"],"agentKnowledge":true},"commands":[{"id":"11111111-2222-3333-4444-555555555555","type":"launch","payload":{"sessionId":"ses_one","mode":"resume"}},{"id":"22222222-2222-3333-4444-555555555555","type":"retry","payload":{"sessionId":"ses_one"}}],"minVersion":"0.0.3"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: queue, Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "v0.0.5"}, Command: func(context.Context, hubclient.V4Command) error { t.Fatal("excluded command executed"); return nil }}
	if err := runner.refreshConsent(context.Background()); err != nil {
		t.Fatal(err)
	}
	results := queue.Results()
	if len(results) != 2 || results[0].Result != "failed" || results[1].Result != "failed" {
		t.Fatalf("results=%+v", results)
	}
}

func TestUpdatePromptAndIdentityPersistAcrossReplacement(t *testing.T) {
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	id := queue.InstallID()
	if err := queue.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 3, MinVersion: "0.0.5", RecommendedVersion: "0.0.6", RecommendedDownloadURL: "https://download.example/app", UpdateRequired: true}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	prompt := reopened.UpdatePrompt()
	if reopened.InstallID() != id || !prompt.Available || !prompt.Required || prompt.DownloadURL != "https://download.example/app" {
		t.Fatalf("identity/prompt = %s %+v", reopened.InstallID(), prompt)
	}
	if err := reopened.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 3, MinVersion: "0.0.5", RecommendedVersion: "0.0.6", RecommendedDownloadURL: "http://unsafe.example/app", RecommendedUpdate: false}); err != nil {
		t.Fatal(err)
	}
	if update := reopened.UpdatePrompt(); update.Available || update.DownloadURL != "" {
		t.Fatalf("stale or unsafe prompt = %+v", update)
	}
}

func TestInterruptedCommandCannotLaunchAgain(t *testing.T) {
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	started, err := queue.StartCommand("11111111-2222-3333-4444-555555555555")
	if err != nil || !started {
		t.Fatalf("start=%v err=%v", started, err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	started, err = reopened.StartCommand("11111111-2222-3333-4444-555555555555")
	if err != nil || started {
		t.Fatalf("duplicate start=%v err=%v", started, err)
	}
	results := reopened.Results()
	if len(results) != 1 || results[0].Error != "execution_interrupted" {
		t.Fatalf("interrupted result=%+v", results)
	}
}

func TestLocalPauseWinsOverFreshServerPolicy(t *testing.T) {
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.3"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: queue, Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "v0.0.5"}, LocalPause: func() bool { return true }}
	if err := runner.refreshConsent(context.Background()); err != ErrPaused {
		t.Fatalf("local pause error=%v", err)
	}
}

// A Hub retry is reported by its own session's outcome, not by whether the
// whole pass ended cleanly: IC-1 found retries reported as failed during a
// large sync (every pass ends at the Hub's active-upload limit) although the
// session synced a pass later.
func TestRetryResultFollowsTheSessionOutcome(t *testing.T) {
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{Key: "done", SessionID: "ses_done", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, SyncedActivity: 1, RevisionID: "rev_done"},
		{Key: "waiting", SessionID: "ses_waiting", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, FailureCode: "rate_limited"},
		{Key: "broken", SessionID: "ses_broken", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, FailureCode: "unreadable_source"},
	}
	if err := queue.Merge(entries, time.UnixMilli(1)); err != nil {
		t.Fatal(err)
	}
	commands := map[string]string{
		"11111111-2222-3333-4444-555555555555": "ses_done",
		"22222222-2222-3333-4444-555555555555": "ses_waiting",
		"33333333-2222-3333-4444-555555555555": "ses_broken",
		"44444444-2222-3333-4444-555555555555": "ses_unknown",
	}
	for id := range commands {
		if _, err := queue.StartCommand(id); err != nil {
			t.Fatal(err)
		}
		queue.HoldCommand(id)
	}
	root := queue.path
	runner := &Runner{Queue: queue, retryCommands: commands}
	if err := runner.finishRetryCommands(); err != nil {
		t.Fatal(err)
	}
	results := map[string]string{}
	for _, result := range queue.Results() {
		results[result.CommandID] = result.Result
	}
	if results["11111111-2222-3333-4444-555555555555"] != "done" || results["33333333-2222-3333-4444-555555555555"] != "failed" ||
		results["44444444-2222-3333-4444-555555555555"] != "failed" || len(results) != 3 {
		t.Fatalf("first pass results=%v", results)
	}
	if _, open := runner.retryCommands["22222222-2222-3333-4444-555555555555"]; !open || len(runner.retryCommands) != 1 {
		t.Fatalf("waiting retry not kept open: %v", runner.retryCommands)
	}
	// A restart forgets the hold, so the open retry reports as interrupted.
	if reopened, err := Open(filepath.Dir(root)); err != nil {
		t.Fatal(err)
	} else {
		interrupted := false
		for _, result := range reopened.Results() {
			interrupted = interrupted || (result.CommandID == "22222222-2222-3333-4444-555555555555" && result.Error == "execution_interrupted")
		}
		if !interrupted {
			t.Fatal("held retry was not reported as interrupted after a restart")
		}
	}
	var waiting Entry
	for _, entry := range queue.Entries() {
		if entry.Key == "waiting" {
			waiting = entry
		}
	}
	waiting.FailureCode, waiting.RevisionID, waiting.SyncedActivity = "", "rev_waiting", 1
	if err := queue.Update(waiting); err != nil {
		t.Fatal(err)
	}
	if err := runner.finishRetryCommands(); err != nil {
		t.Fatal(err)
	}
	for _, result := range queue.Results() {
		if result.CommandID == "22222222-2222-3333-4444-555555555555" && result.Result != "done" {
			t.Fatalf("waiting retry finished as %+v", result)
		}
	}
	if len(runner.retryCommands) != 0 {
		t.Fatalf("settled retries still open: %v", runner.retryCommands)
	}
}

func TestCommandErrorSSHCodes(t *testing.T) {
	cases := []struct {
		reason remote.Reason
		want   string
	}{
		{remote.ReasonAuthentication, "ssh_auth_failed"},
		{remote.ReasonConnectionFailed, "ssh_unreachable"},
		{remote.ReasonRefreshTimeout, "ssh_unreachable"},
		{remote.ReasonHostKeyChanged, "ssh_host_key_changed"},
		{remote.ReasonHostKeyConfirmation, "ssh_host_key_unknown"},
		{remote.ReasonSFTPUnavailable, "ssh_sftp_unavailable"},
		{remote.ReasonInvalidData, "command_failed"},
		{remote.Reason("future_reason"), "command_failed"},
	}
	for _, tc := range cases {
		for _, install := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/install=%t", tc.reason, install), func(t *testing.T) {
				want := tc.want
				if install && want == "command_failed" {
					want = "connector_install_failed"
				}
				err := fmt.Errorf("wrapped: %w", &SSHCommandError{Reason: &tc.reason, Install: install})
				if got := commandError(err); got != want {
					t.Fatalf("code = %q, want %q", got, want)
				}
			})
		}
	}
	for _, reason := range []remote.Reason{"", remote.ReasonHelperMissing, remote.ReasonHelperIncompatible, remote.ReasonHelperInstallation} {
		if got := commandError(&SSHCommandError{Reason: &reason, Install: true}); got != "connector_install_failed" {
			t.Fatalf("install %q code = %q", reason, got)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := commandError(fmt.Errorf("wrapped: %w", err)); got != "command_cancelled" {
			t.Fatalf("cancellation code = %q", got)
		}
	}
	if got := commandError(errors.New("Permission denied (publickey)")); got != "command_failed" {
		t.Fatalf("unclassified code = %q", got)
	}
}

func TestSSHCommandSurvivesSyncPassCancellation(t *testing.T) {
	for _, kind := range []string{"ssh.test", "ssh.install"} {
		t.Run(kind, func(t *testing.T) {
			passContext, cancelPass := context.WithCancel(context.Background())
			defer cancelPass()
			runner := &Runner{
				CommandContext: context.Background(),
				Command: func(ctx context.Context, _ hubclient.V4Command) error {
					cancelPass()
					return ctx.Err()
				},
			}
			if err := runner.runCommand(passContext, hubclient.V4Command{Type: kind}); err != nil {
				t.Fatalf("routine sync cancellation interrupted %s: %v", kind, err)
			}
		})
	}
}

func TestCommandLifecycleCancellationPreservesCause(t *testing.T) {
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	reason := remote.ReasonRefreshTimeout
	runner := &Runner{
		CommandContext: lifecycle,
		Command: func(ctx context.Context, _ hubclient.V4Command) error {
			cancel()
			<-ctx.Done()
			return &SSHCommandError{Reason: &reason, Install: true}
		},
	}
	err := runner.runCommand(lifecycle, hubclient.V4Command{Type: "ssh.install"})
	if !errors.Is(err, context.Canceled) || commandError(err) != "command_cancelled" {
		t.Fatalf("lifecycle cancellation = %v (%s), want command_cancelled", err, commandError(err))
	}
}

func TestCommandSuccessSurvivesLifecycleCancellationRace(t *testing.T) {
	lifecycle, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &Runner{
		CommandContext: lifecycle,
		Command: func(context.Context, hubclient.V4Command) error {
			cancel()
			return nil
		},
	}
	if err := runner.runCommand(lifecycle, hubclient.V4Command{Type: "ssh.install"}); err != nil {
		t.Fatalf("successful command was replaced by lifecycle cancellation: %v", err)
	}
}

func TestUpdateCommandSurvivesPausedPolicyAndReportsWithoutScale(t *testing.T) {
	t.Setenv("COSLASH_SCALE_IMPORT", "0")
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-3333-4444-555555555555"
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	var checks int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks++
		var input struct {
			Results []hubclient.V4CommandResult `json:"results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if checks == 2 && (len(input.Results) != 1 || input.Results[0].CommandID != id || input.Results[0].Result != "in_progress") {
			t.Errorf("update progress missing: %+v", input.Results)
		}
		fmt.Fprintf(w, `{"configVersion":1,"config":{"paused":true,"autoUpdate":false,"deviceOff":true,"leaveOut":[],"agentKnowledge":true},"commands":[{"id":%q,"type":"update","payload":{"attemptId":%q,"targetVersion":"0.0.6","expiresAt":%q}}],"minVersion":"0.0.3"}`, id, id, expires)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: q, Version: "0.0.5", Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"}}
	for range 2 {
		if err := runner.refreshConsent(context.Background()); !errors.Is(err, ErrPaused) {
			t.Fatalf("paused checkin: %v", err)
		}
	}
	if u := q.PendingUpdate(); u == nil || u.ID != id {
		t.Fatalf("attempt: %+v", u)
	}
	_, config, _ := q.Policy()
	if config.AutoUpdate || !config.Paused || !config.DeviceOff {
		t.Fatalf("policy changed: %+v", config)
	}
}

func TestUpdateReportDoesNotRunUploadsOrChangeRetryDeadlines(t *testing.T) {
	now := time.Now().UTC()
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "retry-one", SessionID: "ses_one", Session: hubclient.V4Session{Agent: "codex"}, RetryAt: now.Add(time.Hour), BackoffAttempt: 3}
	if err = q.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	if err = q.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{Paused: true, DeviceOff: true}}); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-3333-4444-555555555555"
	expires := now.Add(time.Hour)
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "0.0.6", "expiresAt": expires})
	command := hubclient.V4Command{ID: id, Type: "update", Payload: payload}
	if err = q.AcceptUpdate(command, "0.0.5", now); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v4/devices/me/update-report" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		var input hubclient.V4CommandResult
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if input.CommandID != id || input.Result != "in_progress" {
			t.Errorf("report %+v", input)
		}
		io.WriteString(w, `{"active":true}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: q, Version: "0.0.5", Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"}}
	before := q.Entries()
	if err = runner.ReportUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := q.Entries()
	if calls != 1 || len(after) != 1 || !after[0].RetryAt.Equal(before[0].RetryAt) || after[0].BackoffAttempt != before[0].BackoffAttempt {
		t.Fatalf("calls=%d before=%+v after=%+v", calls, before, after)
	}
	_, config, _ := q.Policy()
	if !config.Paused || !config.DeviceOff || config.AutoUpdate {
		t.Fatalf("policy changed: %+v", config)
	}
}

func TestConcurrentUpdateReportPreservesNormalCheckInResponse(t *testing.T) {
	now := time.Now().UTC()
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Merge([]Entry{{Key: "session", SessionID: "ses_one", Session: hubclient.V4Session{Agent: "codex"}}}, now); err != nil {
		t.Fatal(err)
	}
	if err = q.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{AutoUpdate: false}}); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-3333-4444-555555555555"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "0.0.6", "expiresAt": now.Add(time.Hour)})
	if err = q.AcceptUpdate(hubclient.V4Command{ID: id, Type: "update", Payload: payload}, "0.0.5", now); err != nil {
		t.Fatal(err)
	}
	arrived := make(chan struct{})
	release := make(chan struct{})
	launches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/devices/me/update-report":
			io.WriteString(w, `{"active":true}`)
		case "/v4/devices/me/check-in":
			close(arrived)
			<-release
			io.WriteString(w, `{"configVersion":2,"config":{"paused":false,"autoUpdate":true,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[{"id":"22222222-2222-3333-4444-555555555555","type":"launch","payload":{"sessionId":"ses_one","mode":"resume"}}],"minVersion":"0.0.3","recommendedVersion":"0.0.6"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: q, Version: "0.0.5", Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"}, Command: func(context.Context, hubclient.V4Command) error { launches++; return nil }}
	done := make(chan error, 1)
	go func() { done <- runner.refreshConsent(context.Background()) }()
	<-arrived
	if err = runner.ReportUpdate(context.Background()); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	version, config, minimum := q.Policy()
	if version != 2 || minimum != "0.0.3" || !config.AutoUpdate || launches != 1 || runner.CheckInGeneration() != 1 || q.PendingUpdate() == nil {
		t.Fatalf("version=%d minimum=%s config=%+v launches=%d generation=%d update=%+v", version, minimum, config, launches, runner.CheckInGeneration(), q.PendingUpdate())
	}
}

func TestUpdateReportRevocationAndReceiptReconciliation(t *testing.T) {
	now := time.Now().UTC()
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-3333-4444-555555555555"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "0.0.6", "expiresAt": now.Add(time.Hour)})
	if err = q.AcceptUpdate(hubclient.V4Command{ID: id, Type: "update", Payload: payload}, "0.0.5", now); err != nil {
		t.Fatal(err)
	}
	if err = q.SetUpdateStage(id, "failed", "update_failed"); err != nil {
		t.Fatal(err)
	}
	rejected := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rejected {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"code":"device_revoked"}`)
			return
		}
		io.WriteString(w, `{"active":false}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: q, Version: "0.0.5", Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"}}
	err = runner.ReportUpdate(context.Background())
	var problem hubclient.V4Problem
	if !errors.As(err, &problem) || problem.Code != "device_revoked" || q.PendingUpdate() == nil {
		t.Fatalf("revocation: %v update=%+v", err, q.PendingUpdate())
	}
	rejected = false
	if err = runner.ReportUpdate(context.Background()); err != nil || q.PendingUpdate() != nil {
		t.Fatalf("failure reconciliation: %v update=%+v", err, q.PendingUpdate())
	}
	if runner.CheckInGeneration() != 0 {
		t.Fatal("receipt-only report changed check-in generation")
	}
}

func TestUpdateReceiptSurvivesOlderHubAndReconcilesAfterUpgrade(t *testing.T) {
	now := time.Now().UTC()
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-4333-8444-555555555555"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "0.0.6", "expiresAt": now.Add(time.Hour)})
	if err = q.AcceptUpdate(hubclient.V4Command{ID: id, Type: "update", Payload: payload}, "0.0.5", now); err != nil {
		t.Fatal(err)
	}
	if err = q.SetUpdateStage(id, "finalizing", ""); err != nil {
		t.Fatal(err)
	}
	// This cached capability is from a newer Hub before rollback.
	if err = q.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 1, Capabilities: []string{"update-completion/v1"}}); err != nil {
		t.Fatal(err)
	}
	upgraded := false
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Results []hubclient.V4CommandResult `json:"results"`
			Queue   map[string]json.RawMessage  `json:"queue"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if _, ok := body.Queue["updateReadyAttemptId"]; ok {
			t.Error("new queue field sent to old Hub")
		}
		if len(body.Results) > 0 {
			received = append(received, body.Results[0].Result)
		}
		if upgraded {
			io.WriteString(w, `{"configVersion":3,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.3","recommendedVersion":"0.0.6","capabilities":["update-completion/v1"]}`)
			return
		}
		io.WriteString(w, `{"configVersion":2,"config":{"paused":true,"deviceOff":true,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.3","recommendedVersion":"0.0.6"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: q, Version: "0.0.6", Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.6"}}
	if err = runner.refreshConsent(context.Background()); !errors.Is(err, ErrPaused) || q.PendingUpdate() == nil {
		t.Fatalf("unconfirmed lost on old Hub: %v", err)
	}
	if err = q.ConfirmUpdateHandoff(id); err != nil {
		t.Fatal(err)
	}
	if err = runner.refreshConsent(context.Background()); !errors.Is(err, ErrPaused) || q.PendingUpdate() == nil {
		t.Fatalf("confirmed lost on old Hub: %v", err)
	}
	q, err = Open(root)
	if err != nil || q.PendingUpdate() == nil {
		t.Fatalf("restart lost attempt: %v", err)
	}
	runner.Queue = q
	upgraded = true
	if err = runner.refreshConsent(context.Background()); err != nil || q.PendingUpdate() != nil {
		t.Fatalf("new Hub did not reconcile: %v", err)
	}
	if len(received) != 3 || received[0] != "in_progress" || received[1] != "done" || received[2] != "done" {
		t.Fatalf("receipts: %v", received)
	}
	if runner.CheckInGeneration() != 3 {
		t.Fatalf("check-in generation=%d", runner.CheckInGeneration())
	}
}

func TestFailedUpdateSurvivesOldHubAndDroppedReport(t *testing.T) {
	now := time.Now().UTC()
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	const id = "10000000-0000-4000-8000-000000000001"
	payload, _ := json.Marshal(map[string]any{"attemptId": id, "targetVersion": "0.0.6", "expiresAt": now.Add(time.Hour)})
	command := hubclient.V4Command{ID: id, Type: "update", Payload: payload}
	if err = q.AcceptUpdate(command, "0.0.5", now); err != nil {
		t.Fatal(err)
	}
	if err = q.SetUpdateStage(id, "failed", "update_failed"); err != nil {
		t.Fatal(err)
	}
	phase := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/devices/me/update-report" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body struct {
			Results []hubclient.V4CommandResult `json:"results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Results) != 1 || body.Results[0].CommandID != id || body.Results[0].Result != "failed" {
			t.Errorf("failure receipt=%+v", body.Results)
		}
		response := map[string]any{"configVersion": 1, "config": map[string]any{"paused": false, "autoUpdate": false, "deviceOff": false, "leaveOut": []string{}, "agentKnowledge": true}, "commands": []hubclient.V4Command{}, "minVersion": "0.0.3", "recommendedVersion": "0.0.6"}
		if phase > 0 {
			response["capabilities"] = []string{"update-completion/v1"}
		}
		if phase == 1 {
			response["commands"] = []hubclient.V4Command{command}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	runner := &Runner{Queue: q, Version: "0.0.5", Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"}}
	if err = runner.ReportUpdate(context.Background()); err == nil {
		t.Fatal("old Hub unexpectedly accepted progress endpoint")
	}
	if err = runner.refreshConsent(context.Background()); err != nil || q.PendingUpdate() == nil || q.PendingUpdate().Stage != "failed" {
		t.Fatalf("old Hub lost failure: %v %+v", err, q.PendingUpdate())
	}
	q, err = Open(root)
	if err != nil || q.PendingUpdate() == nil || q.PendingUpdate().Stage != "failed" {
		t.Fatalf("restart lost failure: %v", err)
	}
	runner.Queue = q
	phase = 1
	if err = runner.refreshConsent(context.Background()); err != nil || q.PendingUpdate() == nil || q.PendingUpdate().Stage != "failed" {
		t.Fatalf("redelivery restarted failure: %v %+v", err, q.PendingUpdate())
	}
	phase = 2
	if err = runner.refreshConsent(context.Background()); err != nil || q.PendingUpdate() != nil {
		t.Fatalf("update-aware acknowledgement: %v %+v", err, q.PendingUpdate())
	}
}
