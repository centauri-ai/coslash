package syncv4

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

const checkInOK = `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"minVersion":"0.0.3"}`

func preparationProblem(code string, retryable bool) error {
	return &sessionbackupproducer.PreparationError{Coverage: sessionbackupproducer.Coverage{
		Problems: []sessionbackupv1.CaptureProblem{{Code: code, MemberID: "private-member", Retryable: retryable}}}}
}

// logQueue opens a queue with one private-looking session entry.
func logQueue(t *testing.T, root, sessionID string, now time.Time) (*Queue, Entry) {
	t.Helper()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{Key: "k-" + sessionID, SessionID: sessionID, Activity: now.UnixMilli(), SourceRevision: "r1",
		Session: hubclient.V4Session{Agent: "codex", Title: "Secret acquisition plan", Repo: "acme/private", CWDLabel: "~/clients/private"}}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	for _, stored := range queue.Entries() {
		if stored.Key == entry.Key {
			return queue, stored
		}
	}
	t.Fatal("entry missing")
	return nil, Entry{}
}

// Every failure the queue records maps to one of the Hub's closed codes, or
// to no line for outcomes that are not failures for the owner.
func TestFailuresMapToTheHubsClosedLogCodes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		want    string
	}{
		{"unreadable", preparationProblem(sessionbackupv1.ProblemUnreadable, false), "unreadable_source"},
		{"unavailable", preparationProblem(sessionbackupv1.ProblemUnavailable, true), "unreadable_source"},
		{"unstable", preparationProblem(sessionbackupv1.ProblemUnstable, true), "transcript_changed_during_read"},
		{"invalid", preparationProblem(sessionbackupv1.ProblemInvalid, false), "malformed_artifact"},
		{"unattributable", preparationProblem(sessionbackupv1.ProblemUnattributable, false), "malformed_artifact"},
		{"unsupported", preparationProblem(sessionbackupv1.ProblemUnsupported, false), "malformed_artifact"},
		{"empty preparation", &sessionbackupproducer.PreparationError{}, "unreadable_source"},
		{"hub too large", hubclient.V4Problem{Code: "too_large"}, "too_large"},
		{"hub body too large", hubclient.V4Problem{Code: "request_too_large"}, "too_large"},
		{"hub space", hubclient.V4Problem{Code: "space_full"}, "space_full"},
		{"hub checksum", hubclient.V4Problem{Code: "hash_mismatch"}, "hash_mismatch"},
		{"hub update", hubclient.V4Problem{Code: "client_update_required"}, "client_update_required"},
		{"hub expired", hubclient.V4Problem{Code: "upload_expired"}, "upload_expired"},
		{"hub lost upload", hubclient.V4Problem{Code: "not_found"}, "upload_expired"},
		{"hub unavailable", hubclient.V4Problem{Code: "temporary_unavailable"}, "server_error"},
		{"hub contract", hubclient.V4Problem{Code: "invalid_request"}, "server_error"},
		{"hub unknown", hubclient.V4Problem{Code: "http_502"}, "server_error"},
		{"local error", errors.New("open /Users/someone/clients/private/session.jsonl: permission denied"), "server_error"},
		{"back-pressure", hubclient.V4Problem{Code: "rate_limited"}, ""},
		{"left out", hubclient.V4Problem{Code: "left_out"}, ""},
		{"deleted in Hub", hubclient.V4Problem{Code: "session_deleted"}, ""},
		{"spool rebuilt", sessionbackupproducer.ErrNotPrepared, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
			queue, entry := logQueue(t, t.TempDir(), "ses_one", now)
			runner := Runner{Version: "0.0.5", Queue: queue, Now: func() time.Time { return now }}
			if err := runner.recordFailure(&entry, tc.failure); err != nil {
				t.Fatal(err)
			}
			lines, _ := queue.PendingLog(now)
			if tc.want == "" {
				if len(lines) != 0 {
					t.Fatalf("logged %+v", lines)
				}
				return
			}
			if len(lines) != 1 || lines[0].Code != tc.want || lines[0].Level != "error" || lines[0].SessionID != "ses_one" ||
				lines[0].Message != logMessages[tc.want] || !lines[0].At.Equal(now) {
				t.Fatalf("lines=%+v want code %q", lines, tc.want)
			}
		})
	}
	// Upload outcomes read back from the Hub's status map the same way.
	for code, want := range map[string]string{"malformed_artifact": "malformed_artifact", "hash_mismatch": "hash_mismatch",
		"space_full": "space_full", "upload_expired": "upload_expired", "worker_reclaimed": "server_error",
		"superseded": "", "client_aborted": "", "session_deleted": ""} {
		if got := hubLogCode(code, nil); got != want {
			t.Errorf("status %q logs %q, want %q", code, got, want)
		}
	}
	for code, message := range logMessages {
		if message == "" || len(message) > 240 || strings.ContainsAny(message, "/\\~") {
			t.Errorf("message for %q is not fixed content-free text: %q", code, message)
		}
	}
}

// A failure recorded before the session has a Hub row is still logged, without
// a session ID; the Hub shows it on the device page.
func TestFailureBeforeTheHubSessionExistsIsLoggedForTheDevice(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, entry := logQueue(t, t.TempDir(), "", now)
	runner := Runner{Queue: queue, Now: func() time.Time { return now }}
	if err := runner.recordFailure(&entry, preparationProblem(sessionbackupv1.ProblemUnreadable, false)); err != nil {
		t.Fatal(err)
	}
	lines, _ := queue.PendingLog(now)
	if len(lines) != 1 || lines[0].SessionID != "" || lines[0].Code != "unreadable_source" {
		t.Fatalf("lines=%+v", lines)
	}
}

// Lines without a session differ only in time, so unsent ones collapse to one
// per code; session lines never collapse.
func TestUnsentDeviceLinesCollapsePerCode(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	for i := range 6 {
		entries = append(entries, Entry{Key: fmt.Sprint("k", i), Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}})
	}
	entries[4].SessionID, entries[5].SessionID = "ses_four", "ses_five"
	if err := queue.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Queue: queue, Now: func() time.Time { return now }}
	for i, entry := range queue.Entries() {
		failure := preparationProblem(sessionbackupv1.ProblemUnreadable, false)
		if i == 3 {
			failure = preparationProblem(sessionbackupv1.ProblemUnstable, true)
		}
		if err := runner.recordFailure(&entry, failure); err != nil {
			t.Fatal(err)
		}
	}
	lines, _ := queue.PendingLog(now)
	var device, session int
	for _, line := range lines {
		if line.SessionID == "" {
			device++
		} else {
			session++
		}
	}
	if device != 2 || session != 2 {
		t.Fatalf("lines=%+v", lines)
	}
	if got := queue.Progress().Failing; got != 6 {
		t.Fatalf("failing=%d", got)
	}
}

// Back-pressure never logs, and a failure that repeats (every pass, or on
// every source change) logs once until the session syncs or the Hub asks for
// a retry.
func TestRepeatedFailuresAndBackPressureDoNotFloodTheLog(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, entry := logQueue(t, t.TempDir(), "", now)
	runner := Runner{Queue: queue, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}), Now: func() time.Time { return now }}
	count := func() int { lines, _ := queue.PendingLog(now); return len(lines) }
	current := func() Entry { return queue.Entries()[0] }
	for range 5 {
		entry = current()
		if err := runner.recordFailure(&entry, hubclient.V4Problem{Code: "rate_limited"}); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 0 {
		t.Fatalf("back-pressure logged %d lines", count())
	}
	for range 3 {
		entry = current()
		if err := runner.recordFailure(&entry, hubclient.V4Problem{Code: "temporary_unavailable"}); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 1 {
		t.Fatalf("repeated failure logged %d lines", count())
	}
	// A new source revision hitting the same failure is not a new line.
	changed := current()
	changed.SourceRevision = "r2"
	if err := queue.Merge([]Entry{changed}, now); err != nil {
		t.Fatal(err)
	}
	entry = current()
	if err := runner.recordFailure(&entry, errors.New("network down")); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("source change relogged: %d lines", count())
	}
	// Once the session has a Hub row, the failure is reported for it.
	entry = current()
	entry.SessionID = "ses_later"
	if err := runner.recordFailure(&entry, errors.New("network down")); err != nil {
		t.Fatal(err)
	}
	if lines, _ := queue.PendingLog(now); len(lines) != 2 || lines[1].SessionID != "ses_later" {
		t.Fatalf("session line missing: %+v", lines)
	}
	// A different failure is a new line, and an owner's retry logs again.
	entry = current()
	if err := runner.recordFailure(&entry, hubclient.V4Problem{Code: "space_full"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := queue.RetrySession("ses_later"); err != nil || !ok {
		t.Fatalf("retry ok=%v err=%v", ok, err)
	}
	entry = current()
	if err := runner.recordFailure(&entry, hubclient.V4Problem{Code: "space_full"}); err != nil {
		t.Fatal(err)
	}
	if count() != 4 {
		t.Fatalf("lines=%d, want 4", count())
	}
	// A completed sync clears it; a failure after that is new.
	entry = current()
	// The entry has no spool, so only the queue update matters here.
	_ = runner.complete(&entry, hubclient.V4Status{State: "completed", SessionID: "ses_later", RevisionID: "rev_1"})
	if got := current(); got.LoggedFailure != "" || got.RevisionID != "rev_1" {
		t.Fatal("completion kept the logged failure")
	}
	// A local error after the revision is recorded is not a sync failure.
	entry = current()
	if err := runner.recordFailure(&entry, errors.New("discard spool")); err != nil {
		t.Fatal(err)
	}
	if count() != 4 {
		t.Fatalf("synced session logged a failure: %d lines", count())
	}
}

type recordedCheckIn struct {
	raw map[string]json.RawMessage
	log []map[string]any
}

// checkInServer answers check-ins with status(n) (0 means OK) and records
// each body.
func checkInServer(t *testing.T, status func(n int, body []byte) int) (*hubclient.Client, *[]recordedCheckIn) {
	t.Helper()
	var calls []recordedCheckIn
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/devices/me/check-in" {
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var call recordedCheckIn
		if err := json.Unmarshal(body, &call.raw); err != nil {
			t.Error(err)
		}
		if raw, ok := call.raw["log"]; ok {
			if err := json.Unmarshal(raw, &call.log); err != nil {
				t.Error(err)
			}
		}
		calls = append(calls, call)
		w.Header().Set("Content-Type", "application/json")
		if code := status(len(calls), body); code != 0 {
			w.WriteHeader(code)
			switch code {
			case http.StatusBadRequest:
				io.WriteString(w, `{"code":"invalid_query"}`)
			case http.StatusForbidden:
				io.WriteString(w, `{"code":"device_revoked"}`)
			}
			return
		}
		io.WriteString(w, checkInOK)
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	return &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.5"}, &calls
}

// Unsent lines survive a restart, go out oldest first on the next check-in,
// stay queued when the check-in fails, and are dropped only after the Hub
// accepts them, so a retry never sends a line twice.
func TestCheckInSendsTheLogOnceAndItSurvivesRestart(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	queue, entry := logQueue(t, root, "ses_first", now)
	runner := Runner{Queue: queue, Now: func() time.Time { return now }}
	if err := runner.recordFailure(&entry, preparationProblem(sessionbackupv1.ProblemUnreadable, false)); err != nil {
		t.Fatal(err)
	}
	second := Entry{Key: "sessionless", Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "claude", Title: "Another secret"}}
	if err := queue.Merge([]Entry{second}, now); err != nil {
		t.Fatal(err)
	}
	for _, stored := range queue.Entries() {
		if stored.Key == "sessionless" {
			runner.Now = func() time.Time { return now.Add(time.Second) }
			if err := runner.recordFailure(&stored, errors.New("open /Users/someone/.claude/projects/secret/x.jsonl: EOF")); err != nil {
				t.Fatal(err)
			}
		}
	}
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	client, calls := checkInServer(t, func(n int, _ []byte) int {
		if n == 1 {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	runner = Runner{Queue: queue, Hub: client, Now: func() time.Time { return now.Add(time.Minute) }}
	if err := runner.refreshConsent(t.Context()); !errors.Is(err, ErrStaleConsent) {
		t.Fatalf("failed check-in err=%v", err)
	}
	if lines, _ := queue.PendingLog(now); len(lines) != 2 {
		t.Fatalf("failed check-in dropped lines: %+v", lines)
	}
	if err := runner.refreshConsent(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runner.refreshConsent(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 {
		t.Fatalf("check-ins=%d", len(*calls))
	}
	for i, call := range (*calls)[:2] {
		want := []map[string]any{
			{"at": "2026-09-29T12:00:00Z", "sessionId": "ses_first", "level": "error", "code": "unreadable_source", "message": "coSlash Local cannot read the session folder."},
			{"at": "2026-09-29T12:00:01Z", "level": "error", "code": "server_error", "message": "coSlash Hub could not store this sync."},
		}
		if got, _ := json.Marshal(call.log); string(got) != mustJSON(t, want) {
			t.Fatalf("check-in %d log=%s", i+1, got)
		}
		body, _ := json.Marshal(call.raw)
		for _, private := range []string{"Secret", "secret", "/Users", "clients", ".jsonl", "private-member"} {
			if strings.Contains(string(body), private) {
				t.Fatalf("check-in %d carried %q: %s", i+1, private, body)
			}
		}
	}
	if _, ok := (*calls)[2].raw["log"]; ok {
		t.Fatalf("accepted lines were sent again: %s", (*calls)[2].raw["log"])
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if lines, _ := reopened.PendingLog(now); len(lines) != 0 {
		t.Fatalf("acknowledged lines survived restart: %+v", lines)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The queue keeps at most maxLog unsent lines, dropping the oldest, and each
// check-in carries at most the Hub's 200, oldest first, without repeats.
func TestSyncLogIsBoundedAndDrainsInHubSizedBatches(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	queue, _ := logQueue(t, root, "ses_bulk", now)
	queue.mu.Lock()
	for i := range maxLog + 5 {
		queue.appendLog(LogLine{At: now.Add(time.Duration(i) * time.Millisecond), Level: "error", Code: "server_error"})
	}
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}
	queue.mu.Unlock()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(queue.state.Log); got != maxLog || queue.state.Log[0].Seq != 6 {
		t.Fatalf("kept %d lines from seq %d", got, queue.state.Log[0].Seq)
	}
	client, calls := checkInServer(t, func(int, []byte) int { return 0 })
	runner := Runner{Queue: queue, Hub: client, Now: func() time.Time { return now.Add(time.Hour) }}
	seen := map[time.Time]bool{}
	var last time.Time
	for range maxLog/maxLogBatch + 1 {
		if err := runner.refreshConsent(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for i, call := range *calls {
		if len(call.log) > maxLogBatch {
			t.Fatalf("check-in %d sent %d lines", i+1, len(call.log))
		}
		for _, line := range call.log {
			at, err := time.Parse(time.RFC3339Nano, line["at"].(string))
			if err != nil || seen[at] || !at.After(last) {
				t.Fatalf("line %v repeated or out of order (err %v)", line["at"], err)
			}
			seen[at], last = true, at
		}
	}
	first, _ := time.Parse(time.RFC3339Nano, (*calls)[0].log[0]["at"].(string))
	if len(seen) != maxLog || !first.Equal(now.Add(5*time.Millisecond)) {
		t.Fatalf("sent %d lines starting %v", len(seen), first)
	}
	if _, ok := (*calls)[len(*calls)-1].raw["log"]; ok {
		t.Fatal("drained log still sent")
	}
}

// Lines past the Hub's retention are dropped, a line dated after a clock was
// set back is sent as now, and a batch the Hub refuses does not stop sync.
func TestLogTheHubWouldRefuseDoesNotStopCheckIn(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	queue, _ := logQueue(t, t.TempDir(), "ses_clock", now)
	queue.mu.Lock()
	queue.appendLog(LogLine{At: now.Add(-30 * 24 * time.Hour), Level: "error", Code: "server_error"})
	queue.appendLog(LogLine{At: now.Add(time.Hour), Level: "error", Code: "too_large"})
	queue.mu.Unlock()
	lines, through := queue.PendingLog(now)
	if len(lines) != 1 || lines[0].Code != "too_large" || !lines[0].At.Equal(now) || through != 2 {
		t.Fatalf("lines=%+v through=%d", lines, through)
	}

	// Negative control: a check-in the Hub refuses with and without the log
	// keeps the lines, since the log was not the problem.
	client, calls := checkInServer(t, func(int, []byte) int { return http.StatusBadRequest })
	runner := Runner{Queue: queue, Hub: client, Now: func() time.Time { return now }}
	if err := runner.refreshConsent(t.Context()); !errors.Is(err, ErrStaleConsent) {
		t.Fatalf("refused check-in err=%v", err)
	}
	if len(*calls) != 2 || (*calls)[0].log == nil || (*calls)[1].log != nil {
		t.Fatalf("calls=%+v", *calls)
	}
	if lines, _ := queue.PendingLog(now); len(lines) != 1 {
		t.Fatalf("lines dropped although the check-in failed: %+v", lines)
	}

	client, calls = checkInServer(t, func(_ int, body []byte) int {
		if strings.Contains(string(body), `"log"`) {
			return http.StatusBadRequest
		}
		return 0
	})
	runner.Hub = client
	if err := runner.refreshConsent(t.Context()); err != nil {
		t.Fatalf("refused log stopped sync: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("check-ins=%d", len(*calls))
	}
	if lines, _ := queue.PendingLog(now); len(lines) != 0 {
		t.Fatalf("refused batch kept: %+v", lines)
	}
	if err := runner.refreshConsent(t.Context()); err != nil || len(*calls) != 3 {
		t.Fatalf("next check-in err=%v calls=%d", err, len(*calls))
	}
}

// A new Hub binding must not report the previous binding's sessions.
func TestRebindDropsUnsentLog(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	queue, entry := logQueue(t, root, "ses_old", now)
	if err := queue.Rebind(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Queue: queue, Now: func() time.Time { return now }}
	if err := runner.recordFailure(&entry, hubclient.V4Problem{Code: "space_full"}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Rebind(strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if lines, _ := reopened.PendingLog(now); len(lines) != 0 || reopened.Entries()[0].LoggedFailure != "" {
		t.Fatalf("rebind kept lines=%+v entry=%+v", lines, reopened.Entries()[0])
	}
}
