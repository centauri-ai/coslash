package syncv4

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/user"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

func TestLocalDiagnosticsCapabilityAndPlanBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q.diagnosticsAccount = func() hubclient.DiagnosticsAccount {
		name, home := "test-user", "~"
		return hubclient.DiagnosticsAccount{Name: &name, Home: &home}
	}
	q.SetDiagnosticsRoots(map[string]string{"~/.claude/projects": "ok", "~/.codex/archived_sessions": "missing"})
	if q.localDiagnostics(now) != nil {
		t.Fatal("old Hub received diagnostics")
	}
	q.state.HubCapabilities = []string{hubclient.CapabilityLocalDiagnostics}
	q.diagnosticsAdvertised = true
	plan := &hubclient.V4ImportPlan{Version: 7, Window: "3d", MaxSessions: 3, MaxSessionsPerAgent: 30}
	q.state.Config.ImportPlan = plan
	q.state.PlanStartedAt = now.Add(-time.Hour).UnixMilli()
	q.state.CatchUpFrozenVersion = 7
	for _, item := range []struct {
		agent, key, failure string
		live                bool
	}{
		{"codex", "a", "session_deleted", false}, {"codex", "b", "", true}, {"claude", "c", "", false},
	} {
		q.state.Entries = append(q.state.Entries, Entry{Key: item.key, Selection: sessionbackupproducer.Selection{SourceKind: "local"}, Session: hubclient.V4Session{Agent: item.agent}, Activity: now.Add(-2 * time.Hour).UnixMilli(), CatchUpPlanVersion: 7, FailureCode: item.failure, Live: item.live, ChangedAt: now.UnixMilli()})
	}
	q.state.Entries = append(q.state.Entries, Entry{Key: "unknown-source", Session: hubclient.V4Session{Agent: "grok"}, Activity: now.Add(-time.Hour).UnixMilli(), CatchUpPlanVersion: 7})
	d := q.localDiagnostics(now)
	if d == nil || d.State != "partial" || d.PlanVersion == nil || *d.PlanVersion != 7 {
		t.Fatalf("snapshot = %+v", d)
	}
	if d.Agents[0].Agent != "claude" || *d.Agents[0].Selected != 1 || d.Agents[0].Eligible != nil || d.Agents[0].Reasons.GlobalCap != nil {
		t.Fatalf("claude = %+v", d.Agents[0])
	}
	c := d.Agents[1]
	if c.Agent != "codex" || *c.Selected != 2 || *c.Reasons.Deleted != 1 || *c.Reasons.Quiet != 1 || c.Roots[0].State != "missing" || c.Roots[1].State != "unknown" {
		t.Fatalf("codex = %+v", c)
	}
	if d.Agents[3].Selected != nil {
		t.Fatal("unknown source was counted as a definite local selection")
	}
	encoded, err := json.Marshal(d)
	if err != nil || len(encoded) > 8192 || strings.Contains(string(encoded), "session_deleted") {
		t.Fatalf("unsafe payload: %v", err)
	}
	q.state.CatchUpFrozenVersion = 6
	d = q.localDiagnostics(now)
	if d.Agents[0].Selected != nil || d.Agents[1].Reasons.Quiet != nil {
		t.Fatal("stale plan selection reported")
	}
	q.state.HubCapabilities = nil
	q.diagnosticsAdvertised = false
	if q.localDiagnostics(now) != nil {
		t.Fatal("capability loss retained diagnostics")
	}
}

func TestDiagnosticsAccountNameIsBounded(t *testing.T) {
	for _, value := range []string{"", "bad\nname", strings.Repeat("x", 129), string([]byte{0xff})} {
		if safeAccountName(value) != nil {
			t.Fatalf("accepted unsafe account")
		}
	}
	if got := safeAccountName("test-user"); got == nil || *got != "test-user" {
		t.Fatal("valid account was rejected")
	}
}

func TestDiagnosticsCapabilityDoesNotCrossBindings(t *testing.T) {
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Rebind(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	q.state.HubCapabilities = []string{hubclient.CapabilityLocalDiagnostics}
	q.diagnosticsAdvertised = true
	if err := q.Rebind(strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if q.localDiagnostics(time.Time{}) != nil {
		t.Fatal("new Hub inherited prior capability")
	}
}

type diagnosticHub struct {
	*planHub
	advertised bool
}

func (h *diagnosticHub) V4CheckIn(ctx context.Context, queue hubclient.V4Queue, version int64, results []hubclient.V4CommandResult, agents []string, logs []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	result, err := h.planHub.V4CheckIn(ctx, queue, version, results, agents, logs)
	if h.advertised {
		result.Capabilities = append(result.Capabilities, hubclient.CapabilityLocalDiagnostics)
	}
	return result, err
}

func TestLocalDiagnosticsSentOnlyAfterHubAdvertisement(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q.diagnosticsAccount = func() hubclient.DiagnosticsAccount { return hubclient.DiagnosticsAccount{} }
	h := &diagnosticHub{planHub: &planHub{withoutScale: true}, advertised: true}
	r := &Runner{Queue: q, Hub: h, Now: func() time.Time { return now }}
	if err := r.refreshConsent(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.checkInQueue[0].LocalDiagnostics != nil {
		t.Fatal("sent on first check-in")
	}
	if err := r.refreshConsent(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.checkInQueue[1].LocalDiagnostics == nil {
		t.Fatal("advertised capability ignored")
	}
	h.advertised = false
	if err := r.refreshConsent(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.refreshConsent(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.checkInQueue[3].LocalDiagnostics != nil {
		t.Fatal("sent after capability removal")
	}
}

func TestDiagnosticsSelectionUsesActualFrozenPlan(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q.diagnosticsAccount = func() hubclient.DiagnosticsAccount { return hubclient.DiagnosticsAccount{} }
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 2, MaxSessionsPerAgent: 2}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}, Capabilities: []string{hubclient.CapabilityLocalDiagnostics}}, now); err != nil {
		t.Fatal(err)
	}
	q.SetActiveSince(now)
	entries := []Entry{}
	for i, agent := range []string{"codex", "codex", "claude"} {
		entries = append(entries, Entry{Key: agent + string(rune('a'+i)), Selection: sessionbackupproducer.Selection{SourceKind: "local"}, Session: hubclient.V4Session{Agent: agent}, Activity: now.Add(-time.Duration(i+1) * time.Hour).UnixMilli()})
	}
	if err := q.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	if err := q.FreezeCatchUp(*plan); err != nil {
		t.Fatal(err)
	}
	d := q.localDiagnostics(now)
	if *d.Agents[0].Selected != 0 || *d.Agents[1].Selected != 2 || d.Agents[0].Reasons.GlobalCap != nil || d.Agents[0].Eligible != nil {
		t.Fatalf("selected counts do not match frozen cap")
	}
}

func TestDiagnosticsEmptyPlanDoesNotInventEligibility(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q.diagnosticsAccount = func() hubclient.DiagnosticsAccount { return hubclient.DiagnosticsAccount{} }
	plan := &hubclient.V4ImportPlan{Version: 1, Window: "3d", MaxSessions: 30, MaxSessionsPerAgent: 30}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Config: hubclient.V4Config{ImportPlan: plan}, Capabilities: []string{hubclient.CapabilityLocalDiagnostics}}, now); err != nil {
		t.Fatal(err)
	}
	q.SetActiveSince(now)
	if err := q.Merge([]Entry{{Key: "old", Selection: sessionbackupproducer.Selection{SourceKind: "local"}, Session: hubclient.V4Session{Agent: "codex"}, Activity: now.Add(-7 * 24 * time.Hour).UnixMilli()}}, now); err != nil {
		t.Fatal(err)
	}
	if err := q.FreezeCatchUp(*plan); err != nil {
		t.Fatal(err)
	}
	d := q.localDiagnostics(now)
	if d.Agents[1].Selected == nil || *d.Agents[1].Selected != 0 || d.Agents[1].Eligible != nil || d.Agents[1].Discovered != nil {
		t.Fatal("unknown discovery was presented as an eligible total")
	}
}

func TestDiagnosticsWireAndCheckInDoNotRescanRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := claude.ProjectsRoot(home)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q.diagnosticsAccount = func() hubclient.DiagnosticsAccount { return hubclient.DiagnosticsAccount{} }
	q.SetDiagnosticsRoots(map[string]string{"~/.claude/projects": "ok"})
	oldWire, err := json.Marshal(hubclient.V4Queue{LocalDiagnostics: q.localDiagnostics(time.Time{})})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(oldWire), "localDiagnostics") {
		t.Fatal("old Hub wire contains diagnostics")
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	q.state.HubCapabilities = []string{hubclient.CapabilityLocalDiagnostics}
	q.diagnosticsAdvertised = true
	d := q.localDiagnostics(time.Time{})
	if d.Agents[0].Roots[0].State != "ok" {
		t.Fatal("check-in re-probed a root instead of using the scheduled snapshot")
	}
	newWire, err := json.Marshal(hubclient.V4Queue{LocalDiagnostics: d})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(newWire), `"localDiagnostics"`) || !strings.Contains(string(newWire), `"observedAt":null`) {
		t.Fatal("advertised diagnostics missing from wire")
	}
}

func TestDiagnosticsRequiresFreshAdvertisementAfterRestart(t *testing.T) {
	root := t.TempDir()
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyPolicyAt(hubclient.V4CheckIn{ConfigVersion: 1, Capabilities: []string{hubclient.CapabilityLocalDiagnostics}}, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	q, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if q.localDiagnostics(time.Time{}) != nil {
		t.Fatal("persisted capability was used before a new check-in")
	}
}

func TestDiagnosticsAccountUsesEffectiveIdentity(t *testing.T) {
	t.Setenv("USER", "misleading")
	called := ""
	lookup := func(id string) (*user.User, error) { called = id; return &user.User{Username: "owner"}, nil }
	current := func() (*user.User, error) { t.Fatal("unexpected current-user fallback"); return nil, nil }
	account := resolveDiagnosticsAccount(42, "/temporary/home", lookup, current)
	if called != "42" || account.Name == nil || *account.Name != "owner" || account.Home == nil || *account.Home != "~" {
		t.Fatalf("identity resolution failed")
	}
	account = resolveDiagnosticsAccount(42, "", func(string) (*user.User, error) { return nil, errors.New("unknown") }, current)
	if account.Name != nil || account.Home != nil {
		t.Fatal("unknown identity or home was invented")
	}
}

func TestDiagnosticsRollbackRecoversWithoutRestart(t *testing.T) {
	t.Setenv("COSLASH_SYNC_POLICY", "1")
	t.Setenv("COSLASH_SCALE_IMPORT", "0")
	var old atomic.Bool
	var rejected atomic.Int32
	var rollbackCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/devices/me/check-in" {
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var input struct {
			Queue   json.RawMessage             `json:"queue"`
			Results []hubclient.V4CommandResult `json:"results"`
			Log     []hubclient.V4LogEntry      `json:"log"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if old.Load() && rollbackCalls.Add(1) <= 2 && (len(input.Results) != 1 || len(input.Log) != 1) {
			t.Error("fallback changed pending results or logs")
		}
		if old.Load() {
			var legacy struct {
				Pending   int             `json:"pending"`
				Failing   int             `json:"failing"`
				Import    json.RawMessage `json:"import"`
				FirstSync json.RawMessage `json:"firstSync"`
				Inventory json.RawMessage `json:"inventory"`
			}
			decoder := json.NewDecoder(bytes.NewReader(input.Queue))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&legacy); err != nil {
				rejected.Add(1)
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"code":"invalid_query"}`)
				return
			}
		}
		capabilities := `"sync-policy/1"`
		if !old.Load() {
			capabilities += `,"local-sync-diagnostics/v1"`
		}
		fmt.Fprintf(w, `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":false,"importPlan":{"version":1,"window":"3d"}},"commands":[],"minVersion":"0.0.0","capabilities":[%s]}`, capabilities)
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue.diagnosticsAccount = func() hubclient.DiagnosticsAccount { return hubclient.DiagnosticsAccount{} }
	client := &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.3.2"}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	runner := &Runner{Queue: queue, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}), Hub: client,
		Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }, Now: func() time.Time { return now }, RequireImportPlan: true}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatalf("initial check-in: %v", err)
	}
	if queue.localDiagnostics(now) == nil {
		t.Fatal("Hub capability was not negotiated")
	}
	queue.appendLog(LogLine{At: now, Level: "error", Code: "server_error"})
	if _, err := queue.StartCommand("cmd_rollback"); err != nil {
		t.Fatal(err)
	}
	if err := queue.FinishCommand(hubclient.V4CommandResult{CommandID: "cmd_rollback", Result: "done"}); err != nil {
		t.Fatal(err)
	}
	old.Store(true)
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatalf("rollback check-in did not recover: %v", err)
	}
	lines, _ := queue.PendingLog(now)
	if rejected.Load() != 1 || rollbackCalls.Load() != 2 || queue.localDiagnostics(now) != nil || len(lines) != 0 || len(queue.Results()) != 0 {
		t.Fatalf("rejected=%d or capability retained", rejected.Load())
	}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatalf("subsequent sync: %v", err)
	}
}
