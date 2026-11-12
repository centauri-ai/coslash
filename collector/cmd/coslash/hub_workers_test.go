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

type workerTestHub struct {
	server             *httptest.Server
	credential         string
	pairCredential     atomic.Value
	expectedCredential atomic.Value
	mode               atomic.Int32
	checkins           chan struct{}
	offline            chan struct{}
	finished           chan struct{}
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
	h := &workerTestHub{credential: credential, checkins: make(chan struct{}, 20), offline: make(chan struct{}, 20), finished: make(chan struct{}, 20), results: make(chan []hubclient.V4CommandResult, 20)}
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
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID string `json:"completeBackupSha256"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(spool, manifest.ID), os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	backup := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool})
	defer backup.Close()
	prepared, err := backup.Open(manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	q, err := syncv4.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := client.V4Binding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Rebind(binding); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SaveDiscoveryCursor(binding, encoded); err != nil {
		t.Fatal(err)
	}
	entry := syncv4.Entry{Key: "synthetic-key", Selection: prepared.Selection, Session: hubclient.V4Session{InstallID: q.InstallID(), LocalKeyHash: "synthetic-key", Agent: "codex", Title: "Synthetic"}, Activity: now.UnixMilli(), BundleID: manifest.ID}
	if err := q.Merge([]syncv4.Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
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
	registry, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.addPrimary(primary.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := registry.addSecondary(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	if err := registry.setEnabled(secondary.server.URL, true); err != nil {
		t.Fatal(err)
	}
	primaryClient, _ := s.client(hubConnection{Origin: primary.server.URL}, true)
	secondaryClient, _ := s.client(hubConnection{Origin: secondary.server.URL}, false)
	secondaryRoot := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(secondary.server.URL))))
	idA, bundleA := seedWorkerQueue(t, filepath.Join(home, "sync-v4"), filepath.Join(home, "session-backups", "prepared"), primaryClient, now, "primary")
	idB, bundleB := seedWorkerQueue(t, filepath.Join(secondaryRoot, "sync-v4"), filepath.Join(secondaryRoot, "prepared"), secondaryClient, now, "secondary")
	if idA == idB || bundleA != bundleB {
		t.Fatal("incorrect fixture identities")
	}
	if err := s.StartEnabled(primary.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := s.StartEnabled(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(primary.server.URL); _ = s.Stop(secondary.server.URL) })
	waitSignal(t, pauseReached)
	waitSignal(t, pauseReached)
	unblockPause()
	if err := s.Stop(primary.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
	for _, hub := range []*workerTestHub{primary, secondary} {
		hub.mu.Lock()
		if hub.creates != 0 {
			t.Error("global pause allowed an upload")
		}
		hub.mu.Unlock()
	}
	paused.Store(false)
	if err := s.StartEnabled(primary.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := s.StartEnabled(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
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
	if err := s.Stop(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
	secondarySlot := s.slot(secondary.server.URL)
	if err := secondarySlot.execute(func() error { secondarySlot.controller = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.StartEnabled(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
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
	secondary.mode.Store(2)
	if !s.Retry(secondary.server.URL) {
		t.Fatal("secondary revocation retry unavailable")
	}
	waitSignal(t, credentials[secondary.server.URL].revoked)
	if credentials[primary.server.URL].deletedCount() != 0 {
		t.Fatal("revocation deleted primary credential")
	}
	drainWorkerSignals(primary.checkins)
	if !s.Retry(primary.server.URL) {
		t.Fatal("primary stopped after secondary revocation")
	}
	waitSignal(t, primary.checkins)
	if err := s.Disable(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := s.StartEnabled(secondary.server.URL); err == nil {
		t.Fatal("disabled secondary restarted")
	}
	sentinel := strings.Repeat("a", 64)
	for _, root := range []string{filepath.Join(home, "session-backups", "prepared"), filepath.Join(secondaryRoot, "prepared")} {
		if err := os.MkdirAll(filepath.Join(root, sentinel), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secondaryBackup := sessionbackupproducer.New(sessionbackupproducer.Options{Root: filepath.Join(secondaryRoot, "prepared")})
	defer secondaryBackup.Close()
	if err := secondaryBackup.Discard(sentinel); err != nil {
		t.Fatal(err)
	}
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
	registry, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.addPrimary(server.URL); err != nil {
		t.Fatal(err)
	}
	credentials := &syncTestCredentials{value: "old"}
	base, _ := url.Parse(server.URL)
	client := &hubclient.Client{BaseURL: base, HTTP: server.Client(), Credentials: credentials, CollectorVersion: "1.2.3"}
	q, err := syncv4.Open(filepath.Join(home, "sync-v4"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := client.V4Binding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Rebind(binding); err != nil {
		t.Fatal(err)
	}
	if err := q.Merge([]syncv4.Entry{{Key: "pending", Session: hubclient.V4Session{Agent: "codex"}, Activity: 1}}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	s, err := newHubWorkerSupervisor(registry, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner {
		return &syncv4.Runner{Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }}
	})
	if err != nil {
		t.Fatal(err)
	}
	s.poll = func(context.Context, *syncLoopControl) {}
	s.client = func(hubConnection, bool) (*hubclient.Client, error) { return client, nil }
	if err := s.StartEnabled(server.URL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(server.URL) })
	waitSignal(t, checkin)
	if err := credentials.Save(context.Background(), "replacement"); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	r, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.addPrimary(primary.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := r.addSecondary(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := r.setEnabled(secondary.server.URL, true); err != nil {
		t.Fatal(err)
	}
	s, err := newHubWorkerSupervisor(r, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner {
		return &syncv4.Runner{Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }}
	})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := s.StartEnabled(secondary.server.URL); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, secondary.checkins)
	releaseStart()
	if err := <-primaryDone; err != nil {
		t.Fatal(err)
	}
}

func TestHubWorkerSecondaryCredentialsAreOriginScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	r, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := newHubWorkerSupervisor(r, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.clientForConnection(hubConnection{Origin: "https://hub.example"}, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.clientForConnection(hubConnection{Origin: "https://hub.example:8443"}, false)
	if err != nil {
		t.Fatal(err)
	}
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
	r, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.addPrimary("https://primary.example"); err != nil {
		t.Fatal(err)
	}
	if err := r.addSecondary("https://secondary.example"); err != nil {
		t.Fatal(err)
	}
	if err := r.setEnabled("https://secondary.example", true); err != nil {
		t.Fatal(err)
	}
	s, err := newHubWorkerSupervisor(r, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	if err != nil {
		t.Fatal(err)
	}
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

func TestHubWorkerStopCancelsPendingCredentialLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	r, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.addPrimary("https://hub.example"); err != nil {
		t.Fatal(err)
	}
	s, err := newHubWorkerSupervisor(r, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	s.client = func(connection hubConnection, _ bool) (*hubclient.Client, error) {
		base, _ := url.Parse(connection.Origin)
		return &hubclient.Client{BaseURL: base, Credentials: blockingWorkerCredentials{started: started}}, nil
	}
	done := make(chan error, 1)
	go func() { done <- s.StartEnabled("https://hub.example") }()
	waitSignal(t, started)
	if err := s.Stop("https://hub.example"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("pending start error = %v", err)
	}
	if s.Retry("https://hub.example") {
		t.Fatal("canceled start is running")
	}
}

func TestHubWorkerDisableCancelsStartAfterEnabledCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	hub := newWorkerTestHub(t, "secondary")
	r, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.addPrimary("https://primary.example"); err != nil {
		t.Fatal(err)
	}
	if err := r.addSecondary(hub.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := r.setEnabled(hub.server.URL, true); err != nil {
		t.Fatal(err)
	}
	store := &racingWorkerCredentials{syncTestCredentials: &syncTestCredentials{value: hub.credential}, entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	base, _ := url.Parse(hub.server.URL)
	seedClient := &hubclient.Client{BaseURL: base, HTTP: hub.server.Client(), Credentials: store.syncTestCredentials, CollectorVersion: "1.2.3"}
	root := filepath.Join(home, "hub-connections", fmt.Sprintf("%x", sha256.Sum256([]byte(hub.server.URL))))
	id, bundle := seedWorkerQueue(t, filepath.Join(root, "sync-v4"), filepath.Join(root, "prepared"), seedClient, time.Unix(1800000000, 0), "pending")
	s, err := newHubWorkerSupervisor(r, "1.2.3", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner {
		return &syncv4.Runner{Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }}
	})
	if err != nil {
		t.Fatal(err)
	}
	s.poll = func(context.Context, *syncLoopControl) {}
	s.client = func(hubConnection, bool) (*hubclient.Client, error) {
		return &hubclient.Client{BaseURL: base, HTTP: hub.server.Client(), Credentials: store, CollectorVersion: "1.2.3"}, nil
	}
	startDone, disableDone := make(chan error, 1), make(chan error, 1)
	go func() { startDone <- s.StartEnabled(hub.server.URL) }()
	waitSignal(t, store.entered)
	go func() { disableDone <- s.Disable(hub.server.URL) }()
	waitSignal(t, store.canceled)
	close(store.release)
	if err := <-startDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("stale start error = %v", err)
	}
	if err := <-disableDone; err != nil {
		t.Fatal(err)
	}
	if s.Retry(hub.server.URL) {
		t.Fatal("disabled worker was running")
	}
	if err := s.StartEnabled(hub.server.URL); err == nil {
		t.Fatal("disabled worker restarted")
	}
	select {
	case <-hub.checkins:
		t.Fatal("stale worker checked in")
	default:
	}
	q, err := syncv4.Open(filepath.Join(root, "sync-v4"))
	if err != nil {
		t.Fatal(err)
	}
	if q.InstallID() != id || len(q.Entries()) != 1 || q.Entries()[0].BundleID != bundle {
		t.Fatal("pending work changed during disable")
	}
	if _, err := os.Stat(filepath.Join(root, "prepared", bundle, "manifest.json")); err != nil {
		t.Fatalf("pending spool lost: %v", err)
	}
}

func TestSecondaryEnableCancelledByDisable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	registry, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.addPrimary("https://primary.example"); err != nil {
		t.Fatal(err)
	}
	if err = registry.addSecondary("https://secondary.example"); err != nil {
		t.Fatal(err)
	}
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	if err != nil {
		t.Fatal(err)
	}
	defer workers.StopAll()
	entered := make(chan struct{})
	workers.client = func(c hubConnection, _ bool) (*hubclient.Client, error) {
		base, _ := url.Parse(c.Origin)
		return &hubclient.Client{BaseURL: base, Credentials: blockingWorkerCredentials{started: entered}}, nil
	}
	pairings := newSecondaryPairings(workers)
	done := make(chan error, 1)
	go func() { done <- pairings.enable(context.Background(), "https://secondary.example", true) }()
	waitSignal(t, entered)
	if err := workers.Disable("https://secondary.example"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale enable succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("enable did not cancel")
	}
	state := registry.snapshot()
	if state.Connections[1].Enabled || state.Connections[1].SyncConsent {
		t.Fatal("disable left secondary authorized")
	}
	if err := workers.StartEnabled("https://secondary.example"); err == nil {
		t.Fatal("disabled worker restarted")
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
	registry, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.addPrimary("https://primary.example"); err != nil {
		t.Fatal(err)
	}
	if err = registry.addSecondary("https://secondary.example"); err != nil {
		t.Fatal(err)
	}
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	if err != nil {
		t.Fatal(err)
	}
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

func TestSecondaryControlsDoNotCancelPrimaryStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COSLASH_V4_SYNC", "1")
	registry, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.addPrimary("https://primary.example"); err != nil {
		t.Fatal(err)
	}
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	if err != nil {
		t.Fatal(err)
	}
	defer workers.StopAll()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slot := workers.slot("https://primary.example")
	slot.startMu.Lock()
	slot.startCancel = cancel
	slot.startMu.Unlock()
	if err := workers.Disable("https://primary.example"); err == nil {
		t.Fatal("primary disable accepted")
	}
	if err := newSecondaryPairings(workers).enable(context.Background(), "https://primary.example", true); err == nil {
		t.Fatal("primary secondary-enable accepted")
	}
	if ctx.Err() != nil {
		t.Fatal("secondary control cancelled primary start")
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
	registry, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.addPrimary("https://primary.example"); err != nil {
		t.Fatal(err)
	}
	hub := newWorkerTestHub(t, "secondary-secret")
	if err = registry.addSecondary(hub.server.URL); err != nil {
		t.Fatal(err)
	}
	if err = registry.setEnabled(hub.server.URL, true); err != nil {
		t.Fatal(err)
	}
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner {
		return &syncv4.Runner{Discover: func(context.Context) ([]*session.Session, error) { return nil, nil }}
	})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := workers.StartEnabled(hub.server.URL); err != nil {
		t.Fatal(err)
	}
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
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("disable did not complete")
	}
}

func TestHubWorkerStatusIncludesContactFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	registry, err := openHubRegistry(home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.addPrimary("https://primary.example"); err != nil {
		t.Fatal(err)
	}
	if err = registry.addSecondary("https://secondary.example"); err != nil {
		t.Fatal(err)
	}
	if err = registry.setEnabled("https://secondary.example", true); err != nil {
		t.Fatal(err)
	}
	workers, err := newHubWorkerSupervisor(registry, "1.0.0", func() bool { return false }, sessionbackupproducer.Options{}, func() *syncv4.Runner { return &syncv4.Runner{} })
	if err != nil {
		t.Fatal(err)
	}
	defer workers.StopAll()
	queue, err := syncv4.Open(filepath.Join(home, "test-queue"))
	if err != nil {
		t.Fatal(err)
	}
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
