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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestTwoHubProductionProcess(t *testing.T) {
	if os.Getenv("COSLASH_TEST_TWO_HUB_CHILD") == "1" {
		os.Args = []string{os.Args[0], "--no-open", "--port", "0"}
		main()
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux device credential storage")
	}
	home := t.TempDir()
	for k, v := range map[string]string{
		"COSLASH_HOME": home, "HOME": home, "USERPROFILE": home, "PATH": home,
		"XDG_CONFIG_HOME": home, "XDG_DATA_HOME": home, "PI_CODING_AGENT_DIR": filepath.Join(home, "empty"),
		"COSLASH_PI_SESSION_ROOTS": "", "PI_CODING_AGENT_SESSION_DIR": "", "GROK_HOME": filepath.Join(home, "grok"), "OPENCODE_DB": filepath.Join(home, "missing.db"),
		"COSLASH_V4_SYNC": "1", "COSLASH_SCALE_IMPORT": "1", "COSLASH_SYNC_PAUSED": "", "COSLASH_SYNC_METERED": "", "COSLASH_SYNC_OFFLINE": "", "COSLASH_SYNC_BATTERY_PERCENT": "100",
		"HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "http_proxy": "", "https_proxy": "", "all_proxy": "",
	} {
		t.Setenv(k, v)
	}
	primary, secondary := newWorkerTestHub(t, "primary-secret"), newWorkerTestHub(t, "secondary-secret")
	primary.minVersion, secondary.minVersion = "0.0.0", "0.0.0"
	secondary.commands = true
	t.Setenv("COSLASH_HUB_URL", primary.server.URL)
	certs := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: primary.server.Certificate().Raw}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secondary.server.Certificate().Raw})...)
	certFile := filepath.Join(home, "roots.pem")
	if err := os.WriteFile(certFile, certs, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certFile)
	primaryURL, _ := url.Parse(primary.server.URL)
	secondaryURL, _ := url.Parse(secondary.server.URL)
	primaryStore := hubclient.FileCredentialStore{Path: filepath.Join(home, "hub-credentials", primaryURL.Host)}
	if err := primaryStore.Save(context.Background(), primary.credential); err != nil {
		t.Fatal(err)
	}
	primaryClient := &hubclient.Client{BaseURL: primaryURL, Credentials: fixedCredential(primary.credential)}
	secondaryClient := &hubclient.Client{BaseURL: secondaryURL, Credentials: fixedCredential(secondary.credential)}
	now := time.Now()
	idA, _ := seedWorkerQueue(t, filepath.Join(home, "sync-v4"), filepath.Join(home, "session-backups", "prepared"), primaryClient, now, "primary")
	secondaryRoot := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(secondary.server.URL))))
	idB, _ := seedWorkerQueue(t, filepath.Join(secondaryRoot, "sync-v4"), filepath.Join(secondaryRoot, "prepared"), secondaryClient, now, "secondary")
	if idA == idB {
		t.Fatal("shared install ID")
	}
	type process struct {
		cmd    *exec.Cmd
		output *bytes.Buffer
		desc   runtimeDescriptor
		token  string
	}
	start := func() *process {
		t.Helper()
		p := &process{output: &bytes.Buffer{}}
		p.cmd = exec.Command(os.Args[0], "-test.run=^TestTwoHubProductionProcess$")
		p.cmd.Env = append(os.Environ(), "COSLASH_TEST_TWO_HUB_CHILD=1")
		p.cmd.Stdout = p.output
		p.cmd.Stderr = p.output
		if err := p.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if p.cmd.ProcessState == nil {
				_ = p.cmd.Process.Kill()
				_ = p.cmd.Wait()
			}
		})
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			data, e := os.ReadFile(filepath.Join(home, "runtime.json"))
			token, te := os.ReadFile(filepath.Join(home, "token"))
			if e == nil && te == nil && json.Unmarshal(data, &p.desc) == nil && p.desc.BaseURL != "" {
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
	request := func(p *process, method, path, body string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, p.desc.BaseURL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Coslash-Token", p.token)
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, data
	}
	stop := func(p *process) {
		t.Helper()
		code, data := request(p, http.MethodPost, "/api/shutdown", "{}")
		if code != http.StatusAccepted {
			t.Fatalf("shutdown %d %s", code, data)
		}
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("exit %v: %s", err, p.output.String())
			}
		case <-time.After(20 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
			t.Fatal("shutdown timeout")
		}
	}
	wait := func(label string, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(30 * time.Second):
			t.Fatalf("timeout waiting for %s", label)
		}
	}
	post := func(p *process, path string, body any) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		code, data := request(p, http.MethodPost, path, string(raw))
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
	stop(p)
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
	if code, data := request(p, http.MethodGet, "/api/hub/v4-update", ""); code != 200 || strings.Contains(string(data), `"available":true`) {
		t.Fatalf("secondary affected updater: %d %s", code, data)
	}
	if primary.installID != idA || secondary.installID != idB {
		t.Fatal("wrong per-Hub install IDs")
	}
	drainWorkerSignals(secondary.checkins)
	post(p, "/api/hub/wake", map[string]string{"hubUrl": secondary.server.URL})
	wait("secondary wake", secondary.checkins)
	data, _ := json.Marshal(map[string]string{"hubUrl": secondary.server.URL})
	if code, _ := request(p, http.MethodPost, "/api/hub/wake", string(data)); code != http.StatusTooManyRequests {
		t.Fatalf("secondary wake rate limit = %d", code)
	}
	drainWorkerSignals(primary.checkins)
	post(p, "/api/hub/wake", map[string]string{"hubUrl": primary.server.URL})
	wait("primary wake independent of secondary rate limit", primary.checkins)
	drainWorkerSignals(secondary.checkins)
	post(p, "/api/hub/onboarding/check-in", map[string]string{"hubUrl": secondary.server.URL})
	wait("secondary check-in request", secondary.checkins)
	code, _ := request(p, http.MethodPost, "/api/hub/wake", `{"hubUrl":"https://unknown.example"}`)
	if code != http.StatusNoContent {
		t.Fatalf("unknown wake status %d", code)
	}
	code, _ = request(p, http.MethodPost, "/api/hub/onboarding/check-in", `{"hubUrl":"https://unknown.example"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("unknown check-in status %d", code)
	}
	if code, body := request(p, http.MethodGet, "/api/sync/status", ""); code != http.StatusOK || strings.Contains(string(body), `"hubSwitch"`) {
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
	if code, _ := request(p, http.MethodPost, "/api/hub/wake", string(data)); code != http.StatusNoContent {
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
	raw = post(p, "/api/hubs/re-pairings", map[string]any{"origin": secondary.server.URL, "consent": true})
	pairing.ID = ""
	if json.Unmarshal(raw, &pairing) != nil || pairing.ID == "" {
		t.Fatalf("recovery pairing %s", raw)
	}
	raw = post(p, "/api/hubs/pairings/"+pairing.ID+"/poll", map[string]any{})
	if !strings.Contains(string(raw), "recovered_disabled") {
		t.Fatalf("recovery outcome %s", raw)
	}
	secondary.mode.Store(0)
	post(p, "/api/hubs/enable", map[string]any{"origin": secondary.server.URL, "consent": true})
	wait("secondary after recovery", secondary.checkins)
	stop(p)
	drainWorkerSignals(primary.checkins)
	drainWorkerSignals(secondary.checkins)
	p = start()
	wait("primary after restart", primary.checkins)
	wait("secondary after restart", secondary.checkins)
	code, data = request(p, http.MethodGet, "/api/hubs/status", "")
	if code != 200 || !strings.Contains(string(data), secondary.server.URL) {
		t.Fatalf("Hub status %d %s", code, data)
	}
	stop(p)
}
