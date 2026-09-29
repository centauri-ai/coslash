package syncv4

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
	_ "modernc.org/sqlite"
)

const fixtureRootID = "11111111-2222-3333-4444-555555555555"
const cursorFixtureID = "11111111-2222-4333-8444-555555555555"

type fixedCredential struct{}

func (fixedCredential) Load(context.Context) (string, error) { return "fixture-device-key", nil }
func (fixedCredential) Save(context.Context, string) error   { return nil }

func fixtureBundle(t *testing.T) (*sessionbackupproducer.Manager, *sessionbackupproducer.Prepared, string) {
	t.Helper()
	fixture := filepath.Join("..", "..", "sessionbackup", "v1", "testdata", "fixtures", "valid", "family")
	data, err := os.ReadFile(filepath.Join(fixture, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		CompleteBackupSHA256 string `json:"completeBackupSha256"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, manifest.CompleteBackupSHA256), os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: root})
	prepared, err := manager.Open(manifest.CompleteBackupSHA256)
	if err != nil {
		t.Fatal(err)
	}
	return manager, prepared, root
}

func TestOpenCodeDiscoveryRetainsSourceIdentity(t *testing.T) {
	items := discoveredEntries([]*session.Session{
		{Agent: "opencode", ID: "shared", StartedAt: 1, LastActivityTime: 2, DetailRevision: "source-revision"},
		{Agent: "codex", ID: "shared", StartedAt: 1, LastActivityTime: 2},
		{Agent: "cursor", ID: "ignored"},
		{Agent: "opencode", ID: "child", ParentSessionID: "shared"},
	}, "install", nil)
	if len(items) != 2 || items[0].Key == items[1].Key || items[0].Selection.Agent != "opencode" ||
		items[0].Selection.SessionID != "shared" || items[0].SourceRevision != "source-revision" {
		t.Fatalf("discovered entries = %#v", items)
	}
}

func TestCodexV4HTTPResumeAndNoUnchangedBytes(t *testing.T) {
	manager, prepared, spool := fixtureBundle(t)
	runV4HTTPResume(t, manager, prepared, spool, "Fixture Codex", true)
}

func TestClaudeV4HTTPRoundTrip(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "project")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(claude.ProjectsRoot(home), "project")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf(`{"sessionId":%q,"type":"user","timestamp":"2026-09-28T11:00:00Z","cwd":%q,"message":{"content":"hello"}}`, fixtureRootID, workspace) + "\n"
	if err := os.WriteFile(filepath.Join(root, fixtureRootID+".jsonl"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool, OpenSource: func(context.Context, sessionbackupproducer.Selection) (sessionbackupproducer.SourceHandle, error) {
		return sessionbackupproducer.SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentClaude, SessionID: fixtureRootID})
	if err != nil {
		t.Fatal(err)
	}
	runV4HTTPResume(t, manager, prepared, spool, "Fixture Claude", false)
}

func runV4HTTPResume(t *testing.T, manager *sessionbackupproducer.Manager, prepared *sessionbackupproducer.Prepared, spool, name string, discover bool) {
	t.Helper()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	source := &session.Session{Agent: prepared.Selection.Agent, ID: fixtureRootID, Name: &name, StartedAt: now.Add(-time.Hour).UnixMilli(), LastActivityTime: now.UnixMilli()}
	testV4HTTPResume(t, manager, prepared, spool, source, discover)
}

func TestCursorIDEAndCLIV4HTTPRoundTrip(t *testing.T) {
	for _, lane := range []string{"cursor-ide", "cursor-cli"} {
		t.Run(lane, func(t *testing.T) {
			manager, prepared, spool := fixtureCursorBundle(t, lane)
			name := "Fixture Cursor"
			now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
			source := &session.Session{Agent: "cursor", ID: cursorFixtureID, Name: &name, Entrypoint: &lane,
				StartedAt: now.Add(-time.Hour).UnixMilli(), LastActivityTime: now.UnixMilli()}
			testV4HTTPResume(t, manager, prepared, spool, source, true)
		})
	}
}

func fixtureCursorBundle(t *testing.T, lane string) (*sessionbackupproducer.Manager, *sessionbackupproducer.Prepared, string) {
	t.Helper()
	home, workspace, spool := t.TempDir(), t.TempDir(), t.TempDir()
	transcript := filepath.Join(home, ".cursor", "projects", "repo", "agent-transcripts", cursorFixtureID, cursorFixtureID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"Build this"}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if lane == "cursor-ide" {
		path := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`CREATE TABLE composerHeaders (composerId TEXT PRIMARY KEY, value TEXT, createdAt INTEGER, lastUpdatedAt INTEGER);
			CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
			t.Fatal(err)
		}
		value := `{"name":"Fixture Cursor","workspaceIdentifier":{"uri":{"fsPath":"` + workspace + `"}}}`
		if _, err := db.Exec(`INSERT INTO composerHeaders VALUES (?, ?, 1700000000000, 1700000001000)`, cursorFixtureID, value); err != nil {
			t.Fatal(err)
		}
	} else {
		path := filepath.Join(home, ".cursor", "chats", "workspace", cursorFixtureID, "store.db")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "meta.json"), []byte(`{"cwd":"`+workspace+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
			t.Fatal(err)
		}
		value := hex.EncodeToString([]byte(`{"agentId":"` + cursorFixtureID + `","name":"Fixture Cursor","createdAt":1700000000000}`))
		if _, err := db.Exec(`INSERT INTO meta VALUES ('0', ?)`, value); err != nil {
			t.Fatal(err)
		}
	}
	manager := sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool, OpenSource: func(context.Context, sessionbackupproducer.Selection) (sessionbackupproducer.SourceHandle, error) {
		return sessionbackupproducer.SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
	prepared, err := manager.Prepare(t.Context(), sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentCursor, SessionID: cursorFixtureID})
	if err != nil {
		t.Fatal(err)
	}
	return manager, prepared, spool
}

func testV4HTTPResume(t *testing.T, manager *sessionbackupproducer.Manager, prepared *sessionbackupproducer.Prepared, spool string, source *session.Session, discover bool) {
	t.Helper()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	queueRoot := t.TempDir()
	queue, err := Open(queueRoot)
	if err != nil {
		t.Fatal(err)
	}
	installID := queue.InstallID()
	entry := Entry{Key: localKey("local", source.Agent, source.ID), Selection: prepared.Selection,
		Session: sessionMetadata(source, installID), Activity: source.LastActivityTime, BundleID: prepared.BundleID}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}

	var server *httptest.Server
	var created hubclient.V4Create
	received := map[string]bool{}
	createCount, putCount, confirmCount, checkInCount := 0, 0, 0, 0
	finalizing, interruptAfterFirst := false, true
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Device fixture-device-key" && !strings.HasPrefix(r.URL.Path, "/blob/") {
			t.Errorf("missing device credential on %s", r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v4/devices/me/check-in":
			checkInCount++
			io.WriteString(w, `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"minVersion":"0.0.3"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v4/uploads":
			createCount++
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Error(err)
			}
			if created.Session.InstallID != installID || created.Session.LocalKeyHash != entry.Key || created.Session.Title != *source.Name || created.Session.Agent != source.Agent {
				t.Errorf("metadata = %+v", created.Session)
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(hubclient.V4Status{UploadID: "upload-1", SessionID: "ses_fixture", State: "open", Missing: missing(created.Manifest, received)})
		case r.Method == http.MethodGet && r.URL.Path == "/v4/uploads/upload-1":
			status := hubclient.V4Status{UploadID: "upload-1", SessionID: "ses_fixture", State: "open", Missing: missing(created.Manifest, received)}
			if finalizing {
				status.State, status.RevisionID = "completed", "rev_fixture"
			}
			json.NewEncoder(w).Encode(status)
		case r.Method == http.MethodPost && r.URL.Path == "/v4/uploads/upload-1/chunks:sign":
			if interruptAfterFirst && confirmCount == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				io.WriteString(w, `{"code":"temporary_unavailable"}`)
				return
			}
			var input struct {
				Coords []struct {
					ArtifactOrdinal int `json:"artifactOrdinal"`
					ChunkOrdinal    int `json:"chunkOrdinal"`
				} `json:"coords"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if len(input.Coords) != 1 {
				t.Errorf("sign coords=%d", len(input.Coords))
				return
			}
			coord := input.Coords[0]
			json.NewEncoder(w).Encode(map[string]any{"urls": []map[string]any{{"artifactOrdinal": coord.ArtifactOrdinal, "chunkOrdinal": coord.ChunkOrdinal,
				"url": server.URL + "/blob/" + strconv.Itoa(coord.ArtifactOrdinal) + "/" + strconv.Itoa(coord.ChunkOrdinal), "headers": map[string]string{"X-Upload-Test": "signed"}}}})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/blob/"):
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-Upload-Test") != "signed" {
				t.Error("signed PUT leaked credential or lost header")
			}
			coord := strings.TrimPrefix(r.URL.Path, "/blob/")
			var a, c int
			if _, err := fmt.Sscanf(coord, "%d/%d", &a, &c); err != nil {
				t.Error(err)
			}
			body, _ := io.ReadAll(r.Body)
			chunk := created.Manifest.Artifacts[a].Chunks[c]
			sum := sha256.Sum256(body)
			if int64(len(body)) != chunk.Bytes || hex.EncodeToString(sum[:]) != chunk.SHA256 {
				t.Error("changed signed chunk")
			}
			putCount++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v4/uploads/upload-1/chunks:confirm":
			var input struct {
				Coords []struct {
					ArtifactOrdinal int `json:"artifactOrdinal"`
					ChunkOrdinal    int `json:"chunkOrdinal"`
				} `json:"coords"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			for _, coord := range input.Coords {
				received[strconv.Itoa(coord.ArtifactOrdinal)+"/"+strconv.Itoa(coord.ChunkOrdinal)] = true
				confirmCount++
			}
			json.NewEncoder(w).Encode(hubclient.V4Status{UploadID: "upload-1", SessionID: "ses_fixture", State: "open", Missing: missing(created.Manifest, received)})
		case r.Method == http.MethodPost && r.URL.Path == "/v4/uploads/upload-1/finalize":
			if len(missing(created.Manifest, received)) != 0 {
				t.Error("finalized with missing chunks")
			}
			finalizing = true
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(hubclient.V4Status{UploadID: "upload-1", SessionID: "ses_fixture", State: "finalizing"})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v4/uploads/upload-1/chunks/") && interruptAfterFirst:
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"code":"temporary_unavailable"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.3"}
	runner := &Runner{Queue: queue, Backup: manager, Hub: client, Discover: func(context.Context) ([]*session.Session, error) {
		if discover {
			return []*session.Session{source}, nil
		}
		return nil, nil
	},
		Conditions: func(context.Context) (bool, int, error) { return false, -1, nil }, Now: func() time.Time { return now }}
	if err := runner.SyncOnce(t.Context()); err == nil {
		t.Fatal("interrupted upload unexpectedly completed")
	}
	if createCount != 1 || putCount != 1 || confirmCount != 1 {
		t.Fatalf("create=%d put=%d confirm=%d", createCount, putCount, confirmCount)
	}
	interruptAfterFirst = false
	queue, err = Open(queueRoot)
	if err != nil {
		t.Fatal(err)
	}
	if queue.InstallID() != installID {
		t.Fatal("install identity changed after restart")
	}
	runner.Queue = queue
	runner.Backup = sessionbackupproducer.New(sessionbackupproducer.Options{Root: spool})
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if createCount != 1 || putCount != len(missing(created.Manifest, nil)) || confirmCount != putCount {
		t.Fatalf("resume resent accepted bytes: create=%d put=%d confirm=%d", createCount, putCount, confirmCount)
	}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if progress := queue.Progress(); progress.FirstSync.RecentDone != 1 || progress.FirstSync.RecentTotal != 1 || progress.Pending != 0 {
		t.Fatalf("progress=%+v", progress)
	}
	if err := runner.SyncOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if createCount != 1 || putCount != confirmCount || checkInCount != 4 {
		t.Fatalf("unchanged upload retried: create=%d put=%d confirm=%d checkins=%d", createCount, putCount, confirmCount, checkInCount)
	}
}

func missing(manifest hubclient.V4Manifest, received map[string]bool) []hubclient.V4Missing {
	result := []hubclient.V4Missing{}
	for _, artifact := range manifest.Artifacts {
		for _, chunk := range artifact.Chunks {
			if received[strconv.Itoa(artifact.Ordinal)+"/"+strconv.Itoa(chunk.Ordinal)] {
				continue
			}
			result = append(result, hubclient.V4Missing{ArtifactOrdinal: artifact.Ordinal, ChunkOrdinal: chunk.Ordinal,
				Offset: chunk.Offset, Bytes: chunk.Bytes, SHA256: chunk.SHA256})
		}
	}
	return result
}

func TestPauseAndLeaveOutPreventCreate(t *testing.T) {
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	entry := Entry{Key: "fixture", Session: hubclient.V4Session{Agent: "codex", Repo: "owner/private", CWDLabel: "~/private"}, Activity: now.UnixMilli()}
	if err := queue.Merge([]Entry{entry}, now); err != nil {
		t.Fatal(err)
	}
	for _, config := range []hubclient.V4Config{{Paused: true}, {DeviceOff: true}} {
		runner := Runner{Queue: queue, Now: func() time.Time { return now }, config: config, checkedAt: now}
		if err := runner.allowed(t.Context(), entry.Session); err != ErrPaused {
			t.Fatalf("config=%+v err=%v", config, err)
		}
	}
	if !leftOut(entry.Session, []string{"owner/*"}) || !leftOut(entry.Session, []string{"~/private"}) {
		t.Fatal("leave-out did not match source")
	}
	runner := Runner{Queue: queue, Now: func() time.Time { return now }, checkedAt: now,
		Conditions: func(context.Context) (bool, int, error) { return true, -1, nil }}
	if err := runner.allowed(t.Context(), entry.Session); err != ErrPaused {
		t.Fatalf("metered err=%v", err)
	}
	runner.Conditions = func(context.Context) (bool, int, error) { return false, 19, nil }
	if err := runner.allowed(t.Context(), entry.Session); err != ErrPaused {
		t.Fatalf("battery err=%v", err)
	}
	runner.Now = func() time.Time { return now.Add(consentAge) }
	if err := runner.allowed(t.Context(), entry.Session); err != ErrStaleConsent {
		t.Fatalf("stale err=%v", err)
	}
}

func TestPausedOrOfflineCheckInSendsNoUpload(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(strconv.FormatBool(offline), func(t *testing.T) {
			var uploads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v4/devices/me/check-in" {
					if offline {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					io.WriteString(w, `{"configVersion":1,"config":{"paused":true,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"minVersion":"0.0.3"}`)
					return
				}
				uploads.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			queue, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			runner := Runner{Queue: queue, Backup: sessionbackupproducer.New(sessionbackupproducer.Options{Root: t.TempDir()}),
				Hub: &hubclient.Client{BaseURL: base, Credentials: fixedCredential{}, CollectorVersion: "0.0.3"},
				Discover: func(context.Context) ([]*session.Session, error) {
					t.Error("discovered before policy")
					return nil, nil
				}}
			if err := runner.SyncOnce(t.Context()); err == nil {
				t.Fatal("sync proceeded without current consent")
			}
			if uploads.Load() != 0 {
				t.Fatal("upload was attempted despite pause/offline")
			}
		})
	}
}

func TestRecentBeforeDurableHistoryNewestFirst(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	items := []Entry{
		{Key: "oldest", Activity: now.Add(-10 * 24 * time.Hour).UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}},
		{Key: "recent", Activity: now.Add(-time.Hour).UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}},
		{Key: "older", Activity: now.Add(-4 * 24 * time.Hour).UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}},
	}
	if err := queue.Merge(items, now); err != nil {
		t.Fatal(err)
	}
	queue, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	entries := queue.Entries()
	if len(entries) != 3 || entries[0].Key != "recent" || entries[1].Key != "older" || entries[2].Key != "oldest" {
		t.Fatalf("priority=%+v", entries)
	}
	if progress := queue.Progress(); progress.FirstSync.RecentTotal != 1 || progress.FirstSync.RecentDone != 0 || progress.FirstSync.HistoryState != "syncing" {
		t.Fatalf("progress=%+v", progress)
	}
}

// A first sync of hundreds of sessions meets the Hub's active-upload limit
// (rate_limited) on every pass. The next pass must follow in seconds, not a
// full interval, and other errors keep the regular interval.
func TestNextSyncDelayRetriesSoonAtTheActiveUploadLimit(t *testing.T) {
	busy := hubclient.V4Problem{Code: "rate_limited"}
	for _, tc := range []struct {
		err  error
		want time.Duration
	}{
		{nil, 5 * time.Minute},
		{busy, busyRetry},
		{errors.Join(busy, errors.New("other entry failed")), busyRetry},
		{fmt.Errorf("create: %w", busy), busyRetry},
		{hubclient.V4Problem{Code: "hash_mismatch"}, 5 * time.Minute},
		{errors.New("server error"), 5 * time.Minute},
	} {
		if got := NextSyncDelay(tc.err, 0, 5*time.Minute); got != tc.want {
			t.Errorf("NextSyncDelay(%v) = %s, want %s", tc.err, got, tc.want)
		}
	}
	// A finalizing upload is recorded on the next pass; history waits for it.
	if got := NextSyncDelay(errors.New("other entry failed"), 3, 5*time.Minute); got != busyRetry {
		t.Fatalf("in-flight uploads waited %s", got)
	}
	if busyRetry >= time.Minute {
		t.Fatalf("busy retry %s is not short", busyRetry)
	}
}

func TestDeferReasonIsClosedAndContentFree(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{hubclient.V4Problem{Code: "rate_limited"}, "hub:rate_limited"},
		{fmt.Errorf("%w: v4 check-in omitted current policy", ErrStaleConsent), "consent_unavailable"},
		{ErrPaused, "paused"},
		{errors.New("open /Users/someone/private/session.jsonl: no such file"), "local_error"},
	} {
		if got := DeferReason(tc.err); got != tc.want {
			t.Errorf("DeferReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestInFlightCountsUploadsWithoutARecordedRevision(t *testing.T) {
	now := time.Now().UTC()
	queue, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	for i := range 3 {
		entries = append(entries, Entry{Key: fmt.Sprintf("codex-%d", i), Activity: now.UnixMilli(), Session: hubclient.V4Session{Agent: "codex"}})
	}
	if err := queue.Merge(entries, now); err != nil {
		t.Fatal(err)
	}
	for i, state := range []struct{ upload, revision string }{{"u1", ""}, {"", "r2"}, {"u3", "r3"}} {
		entry := queue.Entries()[i]
		entry.UploadID, entry.RevisionID = state.upload, state.revision
		if err := queue.Update(entry); err != nil {
			t.Fatal(err)
		}
	}
	if got := queue.InFlight(); got != 1 {
		t.Fatalf("in flight=%d", got)
	}
}
