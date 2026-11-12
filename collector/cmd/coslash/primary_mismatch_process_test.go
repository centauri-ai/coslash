package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/centauri-ai/coslash/collector/internal/web"
)

func TestPrimaryMismatchProcessRecovery(t *testing.T) {
	if runTestLocalChild() {
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux device credential storage")
	}
	home := t.TempDir()
	for k, v := range map[string]string{"COSLASH_HOME": home, "HOME": home, "USERPROFILE": home, "COSLASH_V4_SYNC": "1", "COSLASH_HUB_URL": "", "XDG_CONFIG_HOME": home, "XDG_DATA_HOME": home, "PI_CODING_AGENT_DIR": filepath.Join(home, "empty"), "OPENCODE_DB": filepath.Join(home, "missing.db")} {
		t.Setenv(k, v)
	}
	var contacts atomic.Int64
	checked := make(chan struct{}, 1)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacts.Add(1)
		if r.URL.Path == "/v4/devices/me/check-in" {
			select {
			case checked <- struct{}{}:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"configVersion":1,"config":{"paused":true,"deviceOff":false,"leaveOut":[],"agentKnowledge":true,"importPlan":{"version":1,"window":"all","history":true}},"minVersion":"0.0.3","commands":[],"capabilities":["sync-policy"]}`)
			return
		}
		if r.URL.Path == "/v4/devices/me/wait" {
			<-r.Context().Done()
			return
		}
		http.NotFound(w, r)
	}))
	defer hub.Close()
	origin, err := url.Parse(hub.URL)
	requireHubTestOK(t, err)
	store := hubclient.FileCredentialStore{Path: filepath.Join(home, "hub-credentials", origin.Host)}
	requireHubTestOK(t, store.Save(context.Background(), "original"))
	client := &hubclient.Client{BaseURL: origin, Credentials: store}
	binding, err := client.V4Binding(context.Background())
	requireHubTestOK(t, err)
	queue, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	requireHubTestOK(t, queue.Rebind(binding))
	requireHubTestOK(t, queue.Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1, BundleID: "pending-bundle"}}, time.Unix(1, 0)))
	requireHubTestOK(t, queue.SaveDiscoveryCursor(binding, []byte(`{"pending":true}`)))
	installID := queue.InstallID()
	spool := filepath.Join(home, "session-backups", "prepared", "pending-bundle", "fixture")
	requireHubTestOK(t, os.MkdirAll(filepath.Dir(spool), 0700))
	requireHubTestOK(t, os.WriteFile(spool, []byte("pending"), 0600))
	requireHubTestOK(t, writeStoredHubURL(hub.URL))
	requireHubTestOK(t, store.Save(context.Background(), "wrong"))
	start := func() *testLocalProcess { return startTestLocal(t, home, "") }
	p := start()
	status, body := p.request(http.MethodGet, "/api/sync/status", "{}")
	var syncState syncStatus
	if status != 200 || json.Unmarshal(body, &syncState) != nil || syncState.State != "binding_mismatch" {
		t.Fatalf("status %d: %s", status, body)
	}
	if status, _ := p.request(http.MethodGet, "/api/sessions", "{}"); status != 200 {
		t.Fatalf("collection status %d", status)
	}
	wantUIStatus := http.StatusOK
	if _, err := web.Handler(); err != nil {
		wantUIStatus = http.StatusServiceUnavailable
	}
	if status, _ := p.request(http.MethodGet, "/", "{}"); status != wantUIStatus {
		t.Fatalf("UI status %d, want %d", status, wantUIStatus)
	}
	if status, _ := p.request(http.MethodPost, "/api/sync/retry", "{}"); status != 409 {
		t.Fatalf("retry status %d", status)
	}
	if status, _ := p.request(http.MethodPost, "/api/hub/update", "{}"); status != 409 {
		t.Fatalf("update request status %d", status)
	}
	if status, _ := p.request(http.MethodPost, "/api/hub/pairings", "{}"); status != 409 {
		t.Fatalf("pairing request status %d", status)
	}
	if status, body := p.request(http.MethodGet, "/api/hub/v4-update", "{}"); status != 200 || strings.Contains(string(body), `"available":true`) {
		t.Fatalf("update status %d: %s", status, body)
	}
	if contacts.Load() != 0 {
		t.Fatal("mismatched process contacted Hub")
	}
	p.stop()
	q, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	got, cursor := q.DiscoveryCursor()
	if got != binding || string(cursor) != `{"pending":true}` || q.InstallID() != installID || len(q.Entries()) != 1 || q.Entries()[0].BundleID != "pending-bundle" {
		t.Fatal("pending queue changed during mismatch")
	}
	if data, err := os.ReadFile(spool); err != nil || string(data) != "pending" {
		t.Fatalf("pending spool %q: %v", data, err)
	}
	requireHubTestOK(t, store.Save(context.Background(), "original"))
	p = start()
	select {
	case <-checked:
	case <-time.After(20 * time.Second):
		p.stop()
		t.Fatalf("restored process did not check in: %s", p.output.String())
	}
	status, body = p.request(http.MethodGet, "/api/sync/status", "{}")
	if status != 200 || json.Unmarshal(body, &syncState) != nil || syncState.State == "binding_mismatch" {
		t.Fatalf("recovery status %d: %s", status, body)
	}
	p.stop()
	q, err = syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	got, _ = q.DiscoveryCursor()
	if got != binding || q.InstallID() != installID || len(q.Entries()) != 1 {
		t.Fatal("recovery changed binding or pending work")
	}
	if data, err := os.ReadFile(spool); err != nil || string(data) != "pending" {
		t.Fatalf("recovery spool %q: %v", data, err)
	}
	var wrongContacts atomic.Int64
	wrongHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrongContacts.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer wrongHub.Close()
	t.Setenv("COSLASH_HUB_URL", wrongHub.URL)
	p = start()
	status, body = p.request(http.MethodGet, "/api/sync/status", "{}")
	if status != 200 || json.Unmarshal(body, &syncState) != nil || syncState.State != "binding_mismatch" {
		t.Fatalf("origin mismatch status %d: %s", status, body)
	}
	p.stop()
	if wrongContacts.Load() != 0 {
		t.Fatal("origin mismatch contacted the wrong Hub")
	}
	q, err = syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	got, _ = q.DiscoveryCursor()
	if got != binding || q.InstallID() != installID || len(q.Entries()) != 1 {
		t.Fatal("origin mismatch changed pending work")
	}
	select {
	case <-checked:
	default:
	}
	t.Setenv("COSLASH_HUB_URL", "")
	p = start()
	select {
	case <-checked:
	case <-time.After(20 * time.Second):
		p.stop()
		t.Fatalf("restored origin did not check in: %s", p.output.String())
	}
	p.stop()
	q, err = syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	got, _ = q.DiscoveryCursor()
	if got != binding || q.InstallID() != installID || len(q.Entries()) != 1 {
		t.Fatal("origin recovery changed pending work")
	}
	if data, err := os.ReadFile(spool); err != nil || string(data) != "pending" {
		t.Fatalf("origin recovery spool %q: %v", data, err)
	}
}

// Reuse the test binary so process tests exercise the production entrypoint.
func runTestLocalChild() bool {
	if os.Getenv("COSLASH_TEST_LOCAL_CHILD") != "1" {
		return false
	}
	os.Args = []string{os.Args[0], "--no-open", "--port", "0"}
	main()
	return true
}

type testLocalProcess struct {
	t      *testing.T
	cmd    *exec.Cmd
	output bytes.Buffer
	desc   runtimeDescriptor
	token  string
}

func startTestLocal(t *testing.T, home, binary string) *testLocalProcess {
	t.Helper()
	args := []string{"--no-open", "--port", "0"}
	if binary == "" {
		binary, args = os.Args[0], []string{"-test.run=^" + regexp.QuoteMeta(t.Name()) + "$"}
	}
	p := &testLocalProcess{t: t, cmd: exec.Command(binary, args...)}
	p.cmd.Env = append(os.Environ(), "COSLASH_TEST_LOCAL_CHILD=1")
	p.cmd.Stdout, p.cmd.Stderr = &p.output, &p.output
	requireHubTestOK(t, p.cmd.Start())
	t.Cleanup(func() {
		if p.cmd.ProcessState == nil {
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(filepath.Join(home, "runtime.json"))
		token, tokenErr := os.ReadFile(filepath.Join(home, "token"))
		if readErr == nil && tokenErr == nil && json.Unmarshal(data, &p.desc) == nil && p.desc.BaseURL != "" {
			p.token = strings.TrimSpace(string(token))
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	t.Fatalf("Local did not start: %s", p.output.String())
	return nil
}

func (p *testLocalProcess) request(method, path, body string) (int, []byte) {
	p.t.Helper()
	req, err := http.NewRequest(method, p.desc.BaseURL+path, strings.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("X-Coslash-Token", p.token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		p.t.Fatal(err)
	}
	return resp.StatusCode, data
}

func (p *testLocalProcess) stop() {
	p.t.Helper()
	code, data := p.request(http.MethodPost, "/api/shutdown", "{}")
	if code != http.StatusAccepted {
		p.t.Fatalf("shutdown: %d %s", code, data)
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			p.t.Fatalf("Local exit: %v: %s", err, p.output.String())
		}
	case <-time.After(20 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
		p.t.Fatal("Local did not shut down")
	}
}
