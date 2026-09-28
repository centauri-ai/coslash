package syncv4

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
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

func TestRetryResultRequiresCompletedUpload(t *testing.T) {
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "one", SessionID: "ses_one", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, SyncedActivity: 1, RevisionID: "rev_one"}
	if err := queue.Merge([]Entry{entry}, time.UnixMilli(1)); err != nil {
		t.Fatal(err)
	}
	const first = "11111111-2222-3333-4444-555555555555"
	if _, err := queue.StartCommand(first); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Queue: queue, retryCommands: map[string]string{first: "ses_one"}}
	if err := runner.finishRetryCommands(nil); err != nil {
		t.Fatal(err)
	}
	if got := queue.Results(); len(got) != 1 || got[0].Result != "done" {
		t.Fatalf("completed retry=%+v", got)
	}
	const second = "22222222-2222-3333-4444-555555555555"
	if _, err := queue.StartCommand(second); err != nil {
		t.Fatal(err)
	}
	runner.retryCommands = map[string]string{second: "ses_one"}
	if err := runner.finishRetryCommands(context.Canceled); err != nil {
		t.Fatal(err)
	}
	if got := queue.Results(); len(got) != 2 || got[1].Result != "failed" {
		t.Fatalf("cancelled retry=%+v", got)
	}
}
