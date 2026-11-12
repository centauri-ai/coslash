package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

func requireHubTestOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func workerTestRegistry(t *testing.T, home, primary string) *hubRegistry {
	t.Helper()
	r, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	requireHubTestOK(t, r.adoptLegacyPrimary(primary))
	return r
}

type workerTestHub struct {
	server             *httptest.Server
	credential         string
	pairCredential     atomic.Value
	expectedCredential atomic.Value
	mode               atomic.Int32
	checkins           chan struct{}
	offline            chan struct{}
	finished           chan struct{}
	piFinished         chan struct{}
	currentAgent       string
	results            chan []hubclient.V4CommandResult
	commands           bool
	minVersion         string
	uploadGate         <-chan struct{}
	uploadStarted      chan<- struct{}
	mu                 sync.Mutex
	manifest           hubclient.V4Manifest
	missing            []hubclient.V4Missing
	received           map[string]bool
	bytes              int
	creates            int
	installID          string
}

const workerUpdateID = "11111111-2222-4333-8444-555555555555"

type workerTestCredentials struct {
	*syncTestCredentials
	revoked chan struct{}
}

type blockingWorkerCredentials struct{ started chan struct{} }

func (c blockingWorkerCredentials) Load(ctx context.Context) (string, error) {
	close(c.started)
	<-ctx.Done()
	return "", ctx.Err()
}
func (blockingWorkerCredentials) Save(context.Context, string) error { return nil }
func (blockingWorkerCredentials) Delete(context.Context) error       { return nil }

type racingWorkerCredentials struct {
	*syncTestCredentials
	loads                      atomic.Int32
	entered, canceled, release chan struct{}
}

func (c *racingWorkerCredentials) Load(ctx context.Context) (string, error) {
	if c.loads.Add(1) == 2 {
		close(c.entered)
		<-ctx.Done()
		close(c.canceled)
		<-c.release
	}
	return c.syncTestCredentials.Load(context.Background())
}

func (c *workerTestCredentials) DeleteIfMatches(ctx context.Context, expected string) (bool, error) {
	deleted, err := c.syncTestCredentials.DeleteIfMatches(ctx, expected)
	if deleted {
		c.revoked <- struct{}{}
	}
	return deleted, err
}

func drainWorkerSignals(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func newWorkerTestHub(t *testing.T, credential string) *workerTestHub {
	t.Helper()
	h := &workerTestHub{credential: credential, checkins: make(chan struct{}, 20), offline: make(chan struct{}, 20), finished: make(chan struct{}, 20), piFinished: make(chan struct{}, 20), results: make(chan []hubclient.V4CommandResult, 20)}
	h.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/device-authorizations" {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "pair", "deviceCode": "device-code", "userCode": "ABCD", "verificationUri": "https://" + r.Host + "/approve", "expiresAt": time.Now().Add(time.Minute), "intervalSeconds": 1})
			return
		}
		if r.URL.Path == "/v1/device-authorizations/token" {
			credential := h.credential
			if value := h.pairCredential.Load(); value != nil {
				credential = value.(string)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"deviceId": "device", "credential": credential, "tokenType": "Device", "scope": "ingest"})
			return
		}
		expected := h.credential
		if value := h.expectedCredential.Load(); value != nil {
			expected = value.(string)
		}
		if r.Header.Get("Authorization") != "Device "+expected {
			t.Errorf("wrong credential at %s", r.URL.Path)
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v4/devices/me/wait" {
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/v4/devices/me/check-in" {
			switch h.mode.Load() {
			case 1:
				h.offline <- struct{}{}
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"code":"server_error"}`)
				return
			case 2:
				w.WriteHeader(401)
				_, _ = io.WriteString(w, `{"code":"device_revoked"}`)
				return
			}
			var input struct {
				Results []hubclient.V4CommandResult `json:"results"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if len(input.Results) > 0 {
				h.results <- input.Results
			}
			h.checkins <- struct{}{}
			commands := []hubclient.V4Command{}
			var hosts []hubclient.V4SSHHost
			if h.commands {
				commands = []hubclient.V4Command{{ID: "ssh", Type: "ssh.install"}, {ID: workerUpdateID, Type: "update", Payload: json.RawMessage(`{"attemptId":"11111111-2222-4333-8444-555555555555","targetVersion":"1.2.4","expiresAt":"2099-01-01T00:00:00Z"}`)}}
				hosts = []hubclient.V4SSHHost{{ID: "host", Alias: "fixture"}}
			}
			minimum := h.minVersion
			if minimum == "" {
				minimum = "0.0.3"
			}
			response := map[string]any{"configVersion": 1, "config": map[string]any{"paused": false, "deviceOff": false, "leaveOut": []string{}, "agentKnowledge": true, "importPlan": map[string]any{"version": 1, "window": "all", "history": true}}, "minVersion": minimum, "commands": commands, "capabilities": []string{hubclient.CapabilitySyncPolicy}}
			if hosts != nil {
				response["sshHosts"] = hosts
			}
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v4/uploads" && h.uploadGate != nil {
			h.uploadStarted <- struct{}{}
			select {
			case <-h.uploadGate:
			case <-r.Context().Done():
				return
			}
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v4/sessions:list-batch":
			var input struct {
				Items []hubclient.V4ListItem `json:"items"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			results := make([]hubclient.V4ListResult, 0, len(input.Items))
			for _, item := range input.Items {
				results = append(results, hubclient.V4ListResult{LocalKeyHash: item.LocalKeyHash, SessionID: "session", State: "listed"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		case r.Method == http.MethodPost && r.URL.Path == "/v4/uploads":
			var input hubclient.V4Create
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
				http.Error(w, "bad request", 400)
				return
			}
			h.creates++
			h.currentAgent = input.Session.Agent
			h.installID = input.Session.InstallID
			h.manifest = input.Manifest
			h.missing = nil
			h.received = map[string]bool{}
			for _, a := range input.Manifest.Artifacts {
				for _, c := range a.Chunks {
					h.missing = append(h.missing, hubclient.V4Missing{ArtifactOrdinal: a.Ordinal, ChunkOrdinal: c.Ordinal, Offset: c.Offset, Bytes: c.Bytes, SHA256: c.SHA256})
				}
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(hubclient.V4Status{UploadID: "upload", SessionID: "session", State: "open", Missing: h.missing})
		case r.Method == http.MethodGet && r.URL.Path == "/v4/uploads/upload":
			_ = json.NewEncoder(w).Encode(hubclient.V4Status{UploadID: "upload", SessionID: "session", State: "open", Missing: h.missing})
		case r.URL.Path == "/v4/uploads/upload/chunks:sign":
			w.WriteHeader(http.StatusNotImplemented)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v4/uploads/upload/chunks/"):
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			var artifact, chunk int
			if _, err := fmt.Sscanf(r.URL.Path, "/v4/uploads/upload/chunks/%d/%d", &artifact, &chunk); err != nil || artifact < 0 || artifact >= len(h.manifest.Artifacts) || chunk < 0 || chunk >= len(h.manifest.Artifacts[artifact].Chunks) {
				t.Errorf("invalid chunk coordinates %s", r.URL.Path)
			} else if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != h.manifest.Artifacts[artifact].Chunks[chunk].SHA256 {
				t.Error("chunk hash mismatch")
			}
			h.received[fmt.Sprintf("%d/%d", artifact, chunk)] = true
			h.bytes += len(body)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v4/uploads/upload/chunks:confirm":
			var input struct {
				Coords []struct {
					ArtifactOrdinal int `json:"artifactOrdinal"`
					ChunkOrdinal    int `json:"chunkOrdinal"`
				} `json:"coords"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			confirmed := map[string]bool{}
			for _, coord := range input.Coords {
				key := fmt.Sprintf("%d/%d", coord.ArtifactOrdinal, coord.ChunkOrdinal)
				if !h.received[key] {
					t.Errorf("confirmed unsent chunk %s", key)
				}
				confirmed[key] = true
			}
			remaining := h.missing[:0]
			for _, chunk := range h.missing {
				if !confirmed[fmt.Sprintf("%d/%d", chunk.ArtifactOrdinal, chunk.ChunkOrdinal)] {
					remaining = append(remaining, chunk)
				}
			}
			h.missing = remaining
			_ = json.NewEncoder(w).Encode(hubclient.V4Status{UploadID: "upload", SessionID: "session", State: "open", Missing: h.missing})
		case r.URL.Path == "/v4/uploads/upload/finalize":
			if len(h.missing) != 0 {
				t.Error("finalized with missing chunks")
			}
			_ = json.NewEncoder(w).Encode(hubclient.V4Status{UploadID: "upload", SessionID: "session", State: "completed", RevisionID: "revision"})
			h.finished <- struct{}{}
			if h.currentAgent == "pi" {
				h.piFinished <- struct{}{}
			}
		default:
			t.Errorf("unexpected Hub request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.server.Close)
	return h
}

func seedWorkerQueue(t *testing.T, root, spool string, client *hubclient.Client, now time.Time, cursor string) (string, string) {
	t.Helper()
	fixture := filepath.Join("..", "..", "sessionbackup", "v1", "testdata", "fixtures", "valid", "family")
	data, err := os.ReadFile(filepath.Join(fixture, "manifest.json"))
	requireHubTestOK(t, err)
	var manifest struct {
		ID string `json:"completeBackupSha256"`
	}
	requireHubTestOK(t, json.Unmarshal(data, &manifest))
	requireHubTestOK(t, os.CopyFS(filepath.Join(spool, manifest.ID), os.DirFS(fixture)))
	backup := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool})
	defer backup.Close()
	prepared, err := backup.Open(manifest.ID)
	requireHubTestOK(t, err)
	q, err := syncv4.Open(root)
	requireHubTestOK(t, err)
	binding, err := client.V4Binding(context.Background())
	requireHubTestOK(t, err)
	requireHubTestOK(t, q.Rebind(binding))
	encoded, err := json.Marshal(cursor)
	requireHubTestOK(t, err)
	requireHubTestOK(t, q.SaveDiscoveryCursor(binding, encoded))
	entry := syncv4.Entry{Key: "synthetic-key", Selection: prepared.Selection, Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "synthetic-key", Agent: "codex", Title: "Synthetic"}, Activity: now.UnixMilli(), BundleID: manifest.ID}
	requireHubTestOK(t, q.Merge([]syncv4.Entry{entry}, now))
	return q.InstallID(), manifest.ID
}

func TestHubWorkerSupervisorTwoHubs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	t.Setenv("COSLASH_V4_SYNC", "1")
	t.Setenv("COSLASH_SYNC_POLICY", "1")
	t.Setenv("COSLASH_SYNC_PAUSED", "")
	primary, secondary := newWorkerTestHub(t, "primary-secret"), newWorkerTestHub(t, "secondary-secret")
	secondary.commands = true
	uploadStarted, uploadGate := make(chan struct{}, 2), make(chan struct{})
	primary.uploadStarted, secondary.uploadStarted = uploadStarted, uploadStarted
	primary.uploadGate, secondary.uploadGate = uploadGate, uploadGate
	var releaseUploads sync.Once
	release := func() { releaseUploads.Do(func() { close(uploadGate) }) }
	defer release()
	registry := workerTestRegistry(t, home, primary.server.URL)
	requireHubTestOK(t, registry.addSecondary(secondary.server.URL))
	var paused atomic.Bool
	var sharedEffects atomic.Int32
	paused.Store(true)
	pauseReached := make(chan struct{}, 8)
	releasePause := make(chan struct{})
	var releasePauseOnce sync.Once
	unblockPause := func() { releasePauseOnce.Do(func() { close(releasePause) }) }
	defer unblockPause()
	pause := func() bool {
		if !paused.Load() {
			return false
		}
		select {
		case pauseReached <- struct{}{}:
		default:
		}
		<-releasePause
		return true
	}
	now := time.Unix(1800000000, 0)
	s, err := newHubWorkerSupervisor(registry, "1.2.3", pause, sessionbackupproducer.Options{}, func() *syncv4.Runner {
		return &syncv4.Runner{Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }, Conditions: func(context.Context) (bool, int, error) { return false, -1, nil }, Now: func() time.Time { return now }, Command: func(context.Context, hubclient.V4Command) error { sharedEffects.Add(1); return nil }, ReconcileSSHHosts: func([]hubclient.V4SSHHost) error { sharedEffects.Add(1); return nil }}
	})
	requireHubTestOK(t, err)
	s.poll = func(context.Context, *syncLoopControl) {}
	credentials := map[string]*workerTestCredentials{
		primary.server.URL:   {syncTestCredentials: &syncTestCredentials{value: primary.credential}, revoked: make(chan struct{}, 1)},
		secondary.server.URL: {syncTestCredentials: &syncTestCredentials{value: secondary.credential}, revoked: make(chan struct{}, 1)},
	}
	hubs := map[string]*workerTestHub{primary.server.URL: primary, secondary.server.URL: secondary}
	s.client = func(connection hubConnection, _ bool) (*hubclient.Client, error) {
		base, err := url.Parse(connection.Origin)
		if err != nil {
			return nil, err
		}
		return &hubclient.Client{BaseURL: base, HTTP: hubs[connection.Origin].server.Client(), Credentials: credentials[connection.Origin], CollectorVersion: "1.2.3"}, nil
	}
	if err := s.StartEnabled(secondary.server.URL); err == nil {
		t.Fatal("disabled secondary started")
	}
	requireHubTestOK(t, registry.setEnabled(secondary.server.URL, true))
	primaryClient, _ := s.client(hubConnection{Origin: primary.server.URL}, true)
	secondaryClient, _ := s.client(hubConnection{Origin: secondary.server.URL}, false)
	secondaryRoot := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(secondary.server.URL))))
	idA, bundleA := seedWorkerQueue(t, filepath.Join(home, "sync-v4"), filepath.Join(home, "session-backups", "prepared"), primaryClient, now, "primary")
	idB, bundleB := seedWorkerQueue(t, filepath.Join(secondaryRoot, "sync-v4"), filepath.Join(secondaryRoot, "prepared"), secondaryClient, now, "secondary")
	if idA == idB || bundleA != bundleB {
		t.Fatal("incorrect fixture identities")
	}
	requireHubTestOK(t, s.StartEnabled(primary.server.URL))
	requireHubTestOK(t, s.StartEnabled(secondary.server.URL))
	t.Cleanup(func() { _ = s.Stop(primary.server.URL); _ = s.Stop(secondary.server.URL) })
	waitSignal(t, pauseReached)
	waitSignal(t, pauseReached)
	unblockPause()
	requireHubTestOK(t, s.Stop(primary.server.URL))
	requireHubTestOK(t, s.Stop(secondary.server.URL))
	for _, hub := range []*workerTestHub{primary, secondary} {
		hub.mu.Lock()
		if hub.creates != 0 {
			t.Error("global pause allowed an upload")
		}
		hub.mu.Unlock()
	}
	paused.Store(false)
	requireHubTestOK(t, s.StartEnabled(primary.server.URL))
	requireHubTestOK(t, s.StartEnabled(secondary.server.URL))
	waitSignal(t, uploadStarted)
	waitSignal(t, uploadStarted)
	release()
	waitSignal(t, primary.finished)
	waitSignal(t, secondary.finished)
	for _, hub := range []*workerTestHub{primary, secondary} {
		hub.mu.Lock()
		if hub.creates != 1 || hub.bytes == 0 {
			t.Errorf("sync creates=%d bytes=%d", hub.creates, hub.bytes)
		}
		hub.mu.Unlock()
	}
	if primary.installID != idA || secondary.installID != idB {
		t.Fatalf("wrong queue identities: primary=%s secondary=%s", primary.installID, secondary.installID)
	}
	_, cursorA := s.slot(primary.server.URL).controller.Queue().DiscoveryCursor()
	_, cursorB := s.slot(secondary.server.URL).controller.Queue().DiscoveryCursor()
	if string(cursorA) != `"primary"` || string(cursorB) != `"secondary"` {
		t.Fatalf("cursors = %s, %s", cursorA, cursorB)
	}
	if !s.Retry(secondary.server.URL) {
		t.Fatal("secondary result retry unavailable")
	}
	select {
	case results := <-secondary.results:
		got := map[string]string{}
		for _, result := range results {
			got[result.CommandID] = result.Error
		}
		if got["ssh"] != "command_unavailable" || got[workerUpdateID] != "update_unavailable" {
			t.Fatalf("secondary results = %+v", results)
		}
		if sharedEffects.Load() != 0 || s.slot(secondary.server.URL).controller.Queue().PendingUpdate() != nil {
			t.Fatal("secondary changed shared authority")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("secondary command results not acknowledged")
	}
	requireHubTestOK(t, s.Stop(secondary.server.URL))
	secondarySlot := s.slot(secondary.server.URL)
	requireHubTestOK(t, secondarySlot.execute(func() error { secondarySlot.controller = nil; return nil }))
	requireHubTestOK(t, s.StartEnabled(secondary.server.URL))
	drainWorkerSignals(secondary.checkins)
	if !s.Retry(secondary.server.URL) {
		t.Fatal("secondary retry unavailable after restart")
	}
	waitSignal(t, secondary.checkins)
	if got := s.slot(secondary.server.URL).controller.Queue().InstallID(); got != idB {
		t.Fatalf("secondary install id changed: %q", got)
	}
	if _, cursor := s.slot(secondary.server.URL).controller.Queue().DiscoveryCursor(); string(cursor) != `"secondary"` {
		t.Fatalf("reopened cursor = %s", cursor)
	}
	secondary.mode.Store(1)
	drainWorkerSignals(secondary.offline)
	if !s.Retry(secondary.server.URL) {
		t.Fatal("secondary retry unavailable")
	}
	waitSignal(t, secondary.offline)
	drainWorkerSignals(primary.checkins)
	if !s.Retry(primary.server.URL) {
		t.Fatal("primary retry unavailable")
	}
	waitSignal(t, primary.checkins)
	secondary.mode.Store(0)
	drainWorkerSignals(secondary.checkins)
	if !s.Retry(secondary.server.URL) {
		t.Fatal("secondary recovery unavailable")
	}
	waitSignal(t, secondary.checkins)
	secondaryController := s.slot(secondary.server.URL).controller
	secondaryController.mu.RLock()
	secondaryDone := secondaryController.done
	secondaryController.mu.RUnlock()
	secondary.mode.Store(2)
	if !s.Retry(secondary.server.URL) {
		t.Fatal("secondary revocation retry unavailable")
	}
	waitSignal(t, credentials[secondary.server.URL].revoked)
	waitSignal(t, secondaryDone)
	if s.Retry(secondary.server.URL) {
		t.Fatal("revoked secondary worker remained active")
	}
	if credentials[primary.server.URL].deletedCount() != 0 {
		t.Fatal("revocation deleted primary credential")
	}
	drainWorkerSignals(primary.checkins)
	if !s.Retry(primary.server.URL) {
		t.Fatal("primary stopped after secondary revocation")
	}
	waitSignal(t, primary.checkins)
	requireHubTestOK(t, s.Disable(secondary.server.URL))
	if err := s.StartEnabled(secondary.server.URL); err == nil {
		t.Fatal("disabled secondary restarted")
	}
	sentinel := strings.Repeat("a", 64)
	for _, root := range []string{filepath.Join(home, "session-backups", "prepared"), filepath.Join(secondaryRoot, "prepared")} {
		requireHubTestOK(t, os.MkdirAll(filepath.Join(root, sentinel), 0o700))
	}
	secondaryBackup := sessionbackupproducer.New(sessionbackupproducer.Options{Root: filepath.Join(secondaryRoot, "prepared")})
	defer secondaryBackup.Close()
	requireHubTestOK(t, secondaryBackup.Discard(sentinel))
	if _, err := os.Stat(filepath.Join(home, "session-backups", "prepared", sentinel)); err != nil {
		t.Fatalf("primary spool removed: %v", err)
	}
}

func TestHubWorkerSupervisorStopsStaleBindingWithoutRebindingQueue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	t.Setenv("COSLASH_V4_SYNC", "1")
	t.Setenv("COSLASH_SYNC_PAUSED", "")
	checkin := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/devices/me/check-in" {
			_, _ = io.Copy(io.Discard, r.Body)
			checkin <- struct{}{}
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	registry := workerTestRegistry(t, home, server.URL)
	credentials := &syncTestCredentials{value: "old"}
	base, _ := url.Parse(server.URL)
	client := &hubclient.Client{BaseURL: base, HTTP: server.Client(), Credentials: credentials, CollectorVersion: "1.2.3"}
	q, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	binding, err := client.V4Binding(context.Background())
	requireHubTestOK(t, err)
	requireHubTestOK(t, q.Rebind(binding))
	requireHubTestOK(t, q.Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1}}, time.Unix(1, 0)))
	s, err := newHubWorkerSupervisor(registry, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner {
		return &syncv4.Runner{Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }}
	})
	requireHubTestOK(t, err)
	s.poll = func(context.Context, *syncLoopControl) {}
	s.client = func(hubConnection, bool) (*hubclient.Client, error) { return client, nil }
	requireHubTestOK(t, s.StartEnabled(server.URL))
	t.Cleanup(func() { _ = s.Stop(server.URL) })
	waitSignal(t, checkin)
	requireHubTestOK(t, credentials.Save(context.Background(), "replacement"))
	if err := s.StartEnabled(server.URL); err == nil {
		t.Fatal("replaced credential silently rebound the old queue")
	}
	if got, err := credentials.Load(context.Background()); err != nil || got != "replacement" {
		t.Fatalf("replacement credential = %q, %v", got, err)
	}
	if s.Retry(server.URL) {
		t.Fatal("stale worker remained active")
	}
	reopened, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	requireHubTestOK(t, err)
	stored, _ := reopened.DiscoveryCursor()
	if stored != binding || len(reopened.Entries()) != 1 || reopened.Entries()[0].Key != "pending" {
		t.Fatal("pending primary work was changed")
	}
}

func TestHubWorkerSupervisorSlowStartDoesNotBlockAnotherHub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	primary, secondary := newWorkerTestHub(t, "first"), newWorkerTestHub(t, "second")
	r := workerTestRegistry(t, home, primary.server.URL)
	requireHubTestOK(t, r.addSecondary(secondary.server.URL))
	requireHubTestOK(t, r.setEnabled(secondary.server.URL, true))
	s, err := newHubWorkerSupervisor(r, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner {
		return &syncv4.Runner{Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }}
	})
	requireHubTestOK(t, err)
	s.poll = func(context.Context, *syncLoopControl) {}
	started, release, primaryDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var releaseOnce sync.Once
	releaseStart := func() { releaseOnce.Do(func() { close(release) }) }
	s.client = func(connection hubConnection, primaryConnection bool) (*hubclient.Client, error) {
		hub := secondary
		if primaryConnection {
			hub = primary
			close(started)
			<-release
		}
		base, err := url.Parse(connection.Origin)
		if err != nil {
			return nil, err
		}
		return &hubclient.Client{BaseURL: base, HTTP: hub.server.Client(), Credentials: &syncTestCredentials{value: hub.credential}, CollectorVersion: "1.2.3"}, nil
	}
	go func() { primaryDone <- s.StartEnabled(primary.server.URL) }()
	waitSignal(t, started)
	defer s.StopAll()
	defer releaseStart()
	requireHubTestOK(t, s.StartEnabled(secondary.server.URL))
	waitSignal(t, secondary.checkins)
	releaseStart()
	requireHubTestOK(t, <-primaryDone)
}

func TestHubWorkerSecondaryCredentialsAreOriginScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	r, err := openHubRegistry(home, "", nil)
	requireHubTestOK(t, err)
	s, err := newHubWorkerSupervisor(r, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	requireHubTestOK(t, err)
	first, err := s.clientForConnection(hubConnection{Origin: "https://hub.example"}, false)
	requireHubTestOK(t, err)
	second, err := s.clientForConnection(hubConnection{Origin: "https://hub.example:8443"}, false)
	requireHubTestOK(t, err)
	if runtime.GOOS == "linux" {
		a, okA := first.Credentials.(hubclient.FileCredentialStore)
		b, okB := second.Credentials.(hubclient.FileCredentialStore)
		if !okA || !okB || a.Path == b.Path || a.Path == filepath.Join(home, "hub-credentials", "hub.example") {
			t.Fatal("secondary credential paths are not isolated")
		}
	} else {
		a, okA := first.Credentials.(hubclient.OSKeychain)
		b, okB := second.Credentials.(hubclient.OSKeychain)
		if !okA || !okB || a.Account == b.Account || a.LegacyAccount != "" || b.LegacyAccount != "" {
			t.Fatal("secondary keychain accounts are not isolated")
		}
	}
}

func TestHubWorkerRejectsWrongOriginClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	r := workerTestRegistry(t, home, "https://primary.example")
	requireHubTestOK(t, r.addSecondary("https://secondary.example"))
	requireHubTestOK(t, r.setEnabled("https://secondary.example", true))
	s, err := newHubWorkerSupervisor(r, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	requireHubTestOK(t, err)
	s.client = func(hubConnection, bool) (*hubclient.Client, error) {
		base, _ := url.Parse("https://primary.example")
		return &hubclient.Client{BaseURL: base, Credentials: &syncTestCredentials{value: "primary"}}, nil
	}
	if err := s.StartEnabled("https://secondary.example"); err == nil {
		t.Fatal("wrong-origin credential accepted")
	}
	root := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte("https://secondary.example"))), "sync-v4")
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrong-origin attempt created queue: %v", err)
	}
}

type failSecondCredentialLoad struct{ loads atomic.Int32 }

func (s *failSecondCredentialLoad) Load(context.Context) (string, error) {
	if s.loads.Add(1) > 1 {
		return "", errors.New("load failed")
	}
	return "approved", nil
}
func (*failSecondCredentialLoad) Save(context.Context, string) error { return nil }
func (*failSecondCredentialLoad) Delete(context.Context) error       { return nil }

func TestSecondaryEnableReportsStartFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	registry := workerTestRegistry(t, home, "https://primary.example")
	requireHubTestOK(t, registry.addSecondary("https://secondary.example"))
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	requireHubTestOK(t, err)
	defer workers.StopAll()
	store := &failSecondCredentialLoad{}
	workers.client = func(c hubConnection, _ bool) (*hubclient.Client, error) {
		base, _ := url.Parse(c.Origin)
		return &hubclient.Client{BaseURL: base, Credentials: store}, nil
	}
	if err := newSecondaryPairings(workers).enable(context.Background(), "https://secondary.example", true); err == nil {
		t.Fatal("enable reported success when worker start failed")
	}
	status := workers.statuses()
	if len(status) != 2 || !status[1].Enabled || status[1].State != "start_failed" {
		t.Fatalf("status after failed start: %+v", status)
	}
}

func TestSecondaryControlsRejectPrimaryAndUnknownHubs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	registry := workerTestRegistry(t, home, "https://primary.example")
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	requireHubTestOK(t, err)
	defer workers.StopAll()
	if err := workers.Disable("https://primary.example"); err == nil {
		t.Fatal("primary disable accepted")
	}
	if err := newSecondaryPairings(workers).enable(context.Background(), "https://primary.example", true); err == nil {
		t.Fatal("primary secondary-enable accepted")
	}
	if err := workers.Disable("https://unknown.example"); err == nil {
		t.Fatal("unknown disable accepted")
	}
	workers.mu.Lock()
	_, created := workers.slots["https://unknown.example"]
	workers.mu.Unlock()
	if created {
		t.Fatal("unknown control created worker slot")
	}
}

func TestSecondaryDisableWaitsForInventory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	registry := workerTestRegistry(t, home, "https://primary.example")
	hub := newWorkerTestHub(t, "secondary-secret")
	requireHubTestOK(t, registry.addSecondary(hub.server.URL))
	requireHubTestOK(t, registry.setEnabled(hub.server.URL, true))
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner {
		return &syncv4.Runner{Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }}
	})
	requireHubTestOK(t, err)
	store := &syncTestCredentials{value: hub.credential}
	workers.client = func(c hubConnection, _ bool) (*hubclient.Client, error) {
		base, _ := url.Parse(c.Origin)
		return &hubclient.Client{BaseURL: base, HTTP: hub.server.Client(), Credentials: store}, nil
	}
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer workers.StopAll()
	defer unblock()
	workers.prepare = func(ctx context.Context, _ *syncv4.Runner, _ *syncv4.Queue, _ *hubclient.Client, _ chan struct{}) (chan struct{}, func()) {
		done := make(chan struct{})
		go func() { defer close(done); close(entered); <-ctx.Done(); close(cancelled); <-release }()
		return nil, func() { <-done }
	}
	requireHubTestOK(t, workers.StartEnabled(hub.server.URL))
	waitSignal(t, entered)
	disabled := make(chan error, 1)
	go func() { disabled <- workers.Disable(hub.server.URL) }()
	waitSignal(t, cancelled)
	select {
	case err := <-disabled:
		t.Fatalf("disable returned before inventory stopped: %v", err)
	default:
	}
	unblock()
	select {
	case err := <-disabled:
		requireHubTestOK(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("disable did not complete")
	}
}

func TestHubWorkerStatusIncludesContactFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	registry := workerTestRegistry(t, home, "https://primary.example")
	requireHubTestOK(t, registry.addSecondary("https://secondary.example"))
	requireHubTestOK(t, registry.setEnabled("https://secondary.example", true))
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	requireHubTestOK(t, err)
	defer workers.StopAll()
	queue, err := syncv4.Open(filepath.Join(home, "test-queue"))
	requireHubTestOK(t, err)
	if _, err = queue.RecordHubContact(time.Now(), hubclient.V4Problem{Code: "server_error", HTTPStatus: http.StatusServiceUnavailable}); err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://secondary.example")
	slot := workers.slot(base.String())
	if err := slot.execute(func() error {
		slot.controller = &syncController{queue: queue, client: &hubclient.Client{BaseURL: base}, connected: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	status := workers.statuses()
	if len(status) != 2 || !strings.Contains(status[1].Failure, "temporarily unavailable") || status[0].Failure != "" {
		t.Fatalf("status = %+v", status)
	}
	if _, err = queue.RecordHubContact(time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if status = workers.statuses(); status[1].Failure != "" {
		t.Fatalf("stale failure = %+v", status)
	}
}
