package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestTwoHubProductionProcess(t *testing.T) {
	if runTestLocalChild() {
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux device credential storage")
	}
	home := t.TempDir()
	for k, v := range map[string]string{
		"COSLASH_HOME": home, "HOME": home, "USERPROFILE": home, "PATH": home,
		"XDG_CONFIG_HOME": home, "XDG_DATA_HOME": home, "PI_CODING_AGENT_DIR": filepath.Join(home, "empty"),
		"COSLASH_PI_SESSION_ROOTS": filepath.Join(home, "pi-sessions"), "PI_CODING_AGENT_SESSION_DIR": "", "GROK_HOME": filepath.Join(home, "grok"), "OPENCODE_DB": filepath.Join(home, "missing.db"),
		"COSLASH_V4_SYNC": "1", "COSLASH_SCALE_IMPORT": "1", "COSLASH_SYNC_PAUSED": "", "COSLASH_SYNC_METERED": "", "COSLASH_SYNC_OFFLINE": "", "COSLASH_SYNC_BATTERY_PERCENT": "100",
		"HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "http_proxy": "", "https_proxy": "", "all_proxy": "",
	} {
		t.Setenv(k, v)
	}
	requireHubTestOK(t, os.MkdirAll(filepath.Join(home, "pi-sessions"), 0700))
	primary, secondary := newWorkerTestHub(t, "primary-secret"), newWorkerTestHub(t, "secondary-secret")
	primary.minVersion, secondary.minVersion = "0.0.0", "0.0.0"
	secondary.commands = true
	t.Setenv("COSLASH_HUB_URL", primary.server.URL)
	certs := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: primary.server.Certificate().Raw}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secondary.server.Certificate().Raw})...)
	certFile := filepath.Join(home, "roots.pem")
	requireHubTestOK(t, os.WriteFile(certFile, certs, 0600))
	t.Setenv("SSL_CERT_FILE", certFile)
	primaryURL, _ := url.Parse(primary.server.URL)
	secondaryURL, _ := url.Parse(secondary.server.URL)
	primaryStore := hubclient.FileCredentialStore{Path: filepath.Join(home, "hub-credentials", primaryURL.Host)}
	requireHubTestOK(t, primaryStore.Save(context.Background(), primary.credential))
	primaryClient := &hubclient.Client{BaseURL: primaryURL, Credentials: fixedCredential(primary.credential)}
	secondaryClient := &hubclient.Client{BaseURL: secondaryURL, Credentials: fixedCredential(secondary.credential)}
	now := time.Now()
	idA, _ := seedWorkerQueue(t, filepath.Join(home, "sync-v4"), filepath.Join(home, "session-backups", "prepared"), primaryClient, now, "primary")
	secondaryRoot := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(secondary.server.URL))))
	idB, _ := seedWorkerQueue(t, filepath.Join(secondaryRoot, "sync-v4"), filepath.Join(secondaryRoot, "prepared"), secondaryClient, now, "secondary")
	if idA == idB {
		t.Fatal("shared install ID")
	}
	start := func() *testLocalProcess { return startTestLocal(t, home, "") }
	wait := func(label string, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(30 * time.Second):
			t.Fatalf("timeout waiting for %s", label)
		}
	}
	post := func(p *testLocalProcess, path string, body any) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		code, data := p.request(http.MethodPost, path, string(raw))
		if code < 200 || code >= 300 {
			t.Fatalf("%s: %d %s", path, code, data)
		}
		return data
	}
	t.Setenv("COSLASH_SYNC_PAUSED", "1")
	p := start()
	wait("paused primary check-in", primary.checkins)
	raw := post(p, "/api/hubs/pairings", map[string]any{"origin": secondary.server.URL, "consent": true})
	var pairing struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &pairing) != nil || pairing.ID == "" {
		t.Fatalf("pairing %s", raw)
	}
	raw = post(p, "/api/hubs/pairings/"+pairing.ID+"/poll", map[string]any{})
	if !strings.Contains(string(raw), "paired_disabled") {
		t.Fatalf("approval %s", raw)
	}
	select {
	case <-secondary.checkins:
		t.Fatal("secondary started before sync consent")
	default:
	}
	post(p, "/api/hubs/enable", map[string]any{"origin": secondary.server.URL, "consent": true})
	wait("paused secondary check-in", secondary.checkins)
	p.stop()
	for _, hub := range []*workerTestHub{primary, secondary} {
		hub.mu.Lock()
		pausedCreates := hub.creates
		hub.mu.Unlock()
		if pausedCreates != 0 {
			t.Fatal("global pause allowed an upload")
		}
	}
	t.Setenv("COSLASH_SYNC_PAUSED", "")
	drainWorkerSignals(primary.checkins)
	drainWorkerSignals(secondary.checkins)
	p = start()
	wait("primary upload", primary.finished)
	wait("secondary upload", secondary.finished)
	// Start with no transcript and add one after both collectors are running.
	// This exercises discovery, capture, preparation and upload for each Hub.
	transcript := filepath.Join(home, "pi-sessions", "fresh.jsonl")
	fixture := `{"type":"session","version":3,"id":"fresh-synthetic-pi","cwd":"/tmp","timestamp":"2026-10-01T12:00:00Z"}` + "\n" + `{"type":"message","id":"u","timestamp":"2026-10-01T12:00:01Z","message":{"role":"user","content":"synthetic test"}}` + "\n"
	requireHubTestOK(t, os.WriteFile(transcript, []byte(fixture), 0600))
	post(p, "/api/sync/retry", map[string]any{})
	post(p, "/api/hubs/retry", map[string]string{"origin": secondary.server.URL})
	wait("fresh primary transcript upload", primary.piFinished)
	wait("fresh secondary transcript upload", secondary.piFinished)
	post(p, "/api/hubs/retry", map[string]string{"origin": secondary.server.URL})
	select {
	case results := <-secondary.results:
		codes := map[string]string{}
		for _, result := range results {
			codes[result.CommandID] = result.Error
		}
		if codes["ssh"] != "command_unavailable" || codes[workerUpdateID] != "update_unavailable" {
			t.Fatalf("secondary authority results: %+v", codes)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("secondary command results not reported")
	}
	if code, data := p.request(http.MethodGet, "/api/hub/v4-update", ""); code != 200 || strings.Contains(string(data), `"available":true`) {
		t.Fatalf("secondary affected updater: %d %s", code, data)
	}
	if primary.installID != idA || secondary.installID != idB {
		t.Fatal("wrong per-Hub install IDs")
	}
	drainWorkerSignals(secondary.checkins)
	post(p, "/api/hub/wake", map[string]string{"hubUrl": secondary.server.URL})
	wait("secondary wake", secondary.checkins)
	data, _ := json.Marshal(map[string]string{"hubUrl": secondary.server.URL})
	if code, _ := p.request(http.MethodPost, "/api/hub/wake", string(data)); code != http.StatusTooManyRequests {
		t.Fatalf("secondary wake rate limit = %d", code)
	}
	drainWorkerSignals(primary.checkins)
	post(p, "/api/hub/wake", map[string]string{"hubUrl": primary.server.URL})
	wait("primary wake independent of secondary rate limit", primary.checkins)
	drainWorkerSignals(secondary.checkins)
	post(p, "/api/hub/onboarding/check-in", map[string]string{"hubUrl": secondary.server.URL})
	wait("secondary check-in request", secondary.checkins)
	code, _ := p.request(http.MethodPost, "/api/hub/wake", `{"hubUrl":"https://unknown.example"}`)
	if code != http.StatusNoContent {
		t.Fatalf("unknown wake status %d", code)
	}
	code, _ = p.request(http.MethodPost, "/api/hub/onboarding/check-in", `{"hubUrl":"https://unknown.example"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("unknown check-in status %d", code)
	}
	if code, body := p.request(http.MethodGet, "/api/sync/status", ""); code != http.StatusOK || strings.Contains(string(body), `"hubSwitch"`) {
		t.Fatalf("unknown check-in affected primary: %d %s", code, body)
	}
	drainWorkerSignals(secondary.checkins)
	secondary.mode.Store(1)
	post(p, "/api/hubs/retry", map[string]string{"origin": secondary.server.URL})
	wait("secondary outage", secondary.offline)
	drainWorkerSignals(primary.checkins)
	post(p, "/api/sync/retry", map[string]any{})
	wait("primary during secondary outage", primary.checkins)
	post(p, "/api/hubs/disable", map[string]string{"origin": secondary.server.URL})
	if code, _ := p.request(http.MethodPost, "/api/hub/wake", string(data)); code != http.StatusNoContent {
		t.Fatalf("disabled wake status %d", code)
	}
	secondary.mode.Store(0)
	post(p, "/api/hubs/enable", map[string]any{"origin": secondary.server.URL, "consent": true})
	wait("secondary re-enabled", secondary.checkins)
	secondary.mode.Store(2)
	post(p, "/api/hubs/retry", map[string]string{"origin": secondary.server.URL})
	credentialPath := filepath.Join(secondaryRoot, "credential")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(credentialPath); os.IsNotExist(err) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
		t.Fatal("secondary revocation did not remove its credential")
	}
	drainWorkerSignals(primary.checkins)
	post(p, "/api/sync/retry", map[string]any{})
	wait("primary after secondary revocation", primary.checkins)
	secondary.pairCredential.Store("secondary-new-account")
	raw = post(p, "/api/hubs/re-pairings", map[string]any{"origin": secondary.server.URL, "consent": true})
	pairing.ID = ""
	if json.Unmarshal(raw, &pairing) != nil || pairing.ID == "" {
		t.Fatalf("recovery pairing %s", raw)
	}
	raw = post(p, "/api/hubs/pairings/"+pairing.ID+"/poll", map[string]any{})
	if !strings.Contains(string(raw), "binding_mismatch") {
		t.Fatalf("recovery without new-binding consent %s", raw)
	}
	queuePath := filepath.Join(secondaryRoot, "sync-v4", "queue.json")
	oldQueue, err := os.ReadFile(queuePath)
	requireHubTestOK(t, err)
	oldSpool := filepath.Join(secondaryRoot, "prepared", "quarantine-fixture")
	requireHubTestOK(t, os.MkdirAll(filepath.Dir(oldSpool), 0700))
	requireHubTestOK(t, os.WriteFile(oldSpool, []byte("old-account"), 0600))
	raw = post(p, "/api/hubs/re-pairings", map[string]any{"origin": secondary.server.URL, "consent": true, "newBindingConsent": true})
	pairing.ID = ""
	if json.Unmarshal(raw, &pairing) != nil || pairing.ID == "" {
		t.Fatalf("new-binding pairing %s", raw)
	}
	raw = post(p, "/api/hubs/pairings/"+pairing.ID+"/poll", map[string]any{})
	if !strings.Contains(string(raw), "recovered_new_binding_disabled") {
		t.Fatalf("new-binding recovery %s", raw)
	}
	if got, err := os.ReadFile(queuePath); err != nil || !bytes.Equal(got, oldQueue) {
		t.Fatal("new binding changed old queue")
	}
	if got, err := os.ReadFile(oldSpool); err != nil || string(got) != "old-account" {
		t.Fatal("new binding changed old spool")
	}
	secondary.expectedCredential.Store("secondary-new-account")
	secondary.mode.Store(0)
	post(p, "/api/hubs/enable", map[string]any{"origin": secondary.server.URL, "consent": true})
	wait("secondary after recovery", secondary.checkins)
	p.stop()
	drainWorkerSignals(primary.checkins)
	drainWorkerSignals(secondary.checkins)
	p = start()
	wait("primary after restart", primary.checkins)
	wait("secondary after restart", secondary.checkins)
	code, data = p.request(http.MethodGet, "/api/hubs/status", "")
	if code != 200 || !strings.Contains(string(data), secondary.server.URL) || !strings.Contains(string(data), `"quarantined":1`) {
		t.Fatalf("Hub status %d %s", code, data)
	}
	p.stop()
	if got, err := os.ReadFile(queuePath); err != nil || !bytes.Equal(got, oldQueue) {
		t.Fatal("new account reused old queue")
	}
	if got, err := os.ReadFile(oldSpool); err != nil || string(got) != "old-account" {
		t.Fatal("new account used old spool")
	}
	registryBytes, err := os.ReadFile(filepath.Join(home, "hub-connections", "registry.json"))
	requireHubTestOK(t, err)
	var registryState hubRegistryState
	if json.Unmarshal(registryBytes, &registryState) != nil {
		t.Fatal("invalid registry after recovery")
	}
	var generation string
	for _, connection := range registryState.Connections {
		if connection.Origin == secondary.server.URL {
			generation = connection.Generation
		}
	}
	if !validHubGeneration(generation) {
		t.Fatal("new generation was not persisted")
	}
	newQueueBytes, err := os.ReadFile(filepath.Join(secondaryRoot, "generations", generation, "sync-v4", "queue.json"))
	requireHubTestOK(t, err)
	var newQueue struct {
		InstallID string `json:"installId"`
		Entries   []struct {
			Key string `json:"key"`
		} `json:"entries"`
	}
	if json.Unmarshal(newQueueBytes, &newQueue) != nil || newQueue.InstallID == "" || newQueue.InstallID == idB {
		t.Fatal("new account reused old install ID")
	}
	for _, entry := range newQueue.Entries {
		if entry.Key == "synthetic-key" {
			t.Fatal("old queued work reached new account")
		}
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
