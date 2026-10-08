package hubclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestV4PutChunkUsesSignedURLWithoutDeviceCredential(t *testing.T) {
	const body = "verified chunk"
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.Header.Get("Authorization") != "" {
			t.Fatalf("direct request method/auth = %s/%q", r.Method, r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Upload-Token") != "signed" || r.ContentLength != int64(len(body)) {
			t.Fatalf("direct request headers/length = %+v/%d", r.Header, r.ContentLength)
		}
		got, err := io.ReadAll(r.Body)
		if err != nil || string(got) != body {
			t.Fatalf("direct body = %q, err=%v", got, err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer direct.Close()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/uploads/upload/chunks:sign" || r.Header.Get("Authorization") != "Device credential" {
			t.Fatalf("sign request = %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		io.WriteString(w, `{"urls":[{"artifactOrdinal":0,"chunkOrdinal":0,"url":"`+direct.URL+`/signed","headers":{"X-Upload-Token":"signed"},"expiresAt":"2026-09-28T12:00:00Z"}]}`)
	}))
	defer hub.Close()
	base, _ := url.Parse(hub.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}}
	if err := client.V4PutChunk(context.Background(), "upload", V4Missing{ArtifactOrdinal: 0, ChunkOrdinal: 0, Bytes: int64(len(body))}, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
}

func TestV4CheckInReportsPlatformQueueAndAppliedPolicyVersion(t *testing.T) {
	t.Setenv("COSLASH_SCALE_IMPORT", "1")
	t.Setenv("COSLASH_V4_SYNC", "1")
	t.Setenv("COSLASH_SYNC_POLICY", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v4/devices/me/check-in" || r.Header.Get("Authorization") != "Device credential" {
			t.Fatalf("check-in request = %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var input struct {
			OS                   string   `json:"os"`
			InstallChannel       string   `json:"installChannel"`
			AppliedConfigVersion int64    `json:"appliedConfigVersion"`
			Capabilities         []string `json:"capabilities"`
			AgentsFound          []string `json:"agentsFound"`
			Queue                V4Queue  `json:"queue"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.OS != runtime.GOOS || input.InstallChannel != "script" || input.AppliedConfigVersion != 7 || strings.Join(input.Capabilities, ",") != "sync-v4,session-backup/v1,launch,ssh-relay,scale-import/v1,sync-policy/1" || strings.Join(input.AgentsFound, ",") != "claude,codex,cursor" ||
			input.Queue.FirstSync.RecentDone != 2 || input.Queue.FirstSync.RecentTotal != 3 || input.Queue.FirstSync.HistoryState != "syncing" {
			t.Fatalf("check-in input = %+v", input)
		}
		io.WriteString(w, `{"configVersion":8,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.5","recommendedVersion":"0.0.5","nextCheckInSeconds":300}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: "v0.0.5", InstallChannel: "script"}
	var queue V4Queue
	queue.Pending = 1
	queue.FirstSync.RecentDone, queue.FirstSync.RecentTotal, queue.FirstSync.HistoryState = 2, 3, "syncing"
	result, err := client.V4CheckIn(context.Background(), queue, 7, nil, []string{"claude", "codex", "cursor"}, nil)
	if err != nil || result.ConfigVersion != 8 {
		t.Fatalf("check-in result=%+v err=%v", result, err)
	}
}

func TestV4CheckInReportsNormalizedSemverVersion(t *testing.T) {
	for _, want := range []string{"0.0.0-dev", "1.2.3-rc.1+build.4"} {
		t.Run(want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					ClientVersion string `json:"clientVersion"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				if input.ClientVersion != want {
					t.Errorf("reported client version=%q, want %q", input.ClientVersion, want)
				}
				io.WriteString(w, `{"configVersion":0,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.0","nextCheckInSeconds":60}`)
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: want}
			if _, err := client.V4CheckIn(context.Background(), V4Queue{}, 0, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClientVersionLessUsesSemverPrereleaseOrdering(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        bool
	}{
		{"1.2.3-rc.1", "1.2.3", true},
		{"1.2.3-alpha.2", "1.2.3-alpha.10", true},
		{"1.2.3-alpha", "1.2.3-1", false},
		{"1.2.3+one", "1.2.3+two", false},
		{"1.2.3", "1.2.4-dev", true},
	} {
		if got := clientVersionLess(test.left, test.right); got != test.want {
			t.Errorf("clientVersionLess(%q,%q)=%t, want %t", test.left, test.right, got, test.want)
		}
	}
}

func TestV4CheckInScaleSwitchOmitsCapability(t *testing.T) {
	t.Setenv("COSLASH_SCALE_IMPORT", "0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Capabilities []string `json:"capabilities"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(input.Capabilities, "scale-import/v1") {
			t.Fatal("scale capability advertised while disabled")
		}
		io.WriteString(w, `{"configVersion":0,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.5"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: "v0.0.5"}
	if _, err := client.V4CheckIn(context.Background(), V4Queue{}, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSyncCapabilitiesDefaultOnAndCanBeDisabled(t *testing.T) {
	t.Setenv("COSLASH_V4_SYNC", "")
	t.Setenv("COSLASH_SYNC_POLICY", "")
	if !V4SyncEnabled() || !SyncPolicyEnabled() ||
		!slices.Contains(localCapabilities(), "sync-v4") || !slices.Contains(localCapabilities(), CapabilitySyncPolicy) {
		t.Fatal("sync and policy capabilities should be enabled by default")
	}
	t.Setenv("COSLASH_V4_SYNC", "0")
	t.Setenv("COSLASH_SYNC_POLICY", "0")
	capabilities := localCapabilities()
	if slices.Contains(capabilities, "sync-v4") || slices.Contains(capabilities, CapabilitySyncPolicy) {
		t.Fatalf("disabled sync capabilities are still advertised: %v", capabilities)
	}
}

func TestV4ImportWritesRequiredNulls(t *testing.T) {
	encoded, err := json.Marshal(V4Import{Phase: "awaiting_plan"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"current", "lastProgressAt", "rate", "historyCursorAt", "wait"} {
		if string(fields[name]) != "null" {
			t.Fatalf("%s = %s, want explicit null", name, fields[name])
		}
	}
}

func TestV4ImportEveryPhaseMatchesGoldenPayloadShape(t *testing.T) {
	var fixture struct {
		Queue struct {
			Import map[string]json.RawMessage `json:"import"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(readScaleFixture(t, "check-in-import-progress.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"inventory", "awaiting_plan", "warm_start", "listing", "recent", "history", "complete", "paused"} {
		t.Run(phase, func(t *testing.T) {
			encoded, err := json.Marshal(V4Import{Phase: phase})
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != len(fixture.Queue.Import) {
				t.Fatalf("import fields = %s, fixture has %d fields", encoded, len(fixture.Queue.Import))
			}
			for key := range fixture.Queue.Import {
				if _, ok := fields[key]; !ok {
					t.Fatalf("%s missing from %s", key, encoded)
				}
			}
		})
	}
}

func TestV4CheckInRetainsRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"code":"rate_limited"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: "v0.0.5"}
	_, err := client.V4CheckIn(context.Background(), V4Queue{}, 0, nil, nil, nil)
	var problem V4Problem
	if !errors.As(err, &problem) || problem.Code != "rate_limited" || problem.RetryAfter != time.Minute {
		t.Fatalf("rate limit = %v", err)
	}
}

// A new owner's policy is version 0 until they first save settings; pairing
// alone must be enough for the first sync to start.
func TestV4CheckInAcceptsTheDefaultPolicyVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"configVersion":0,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.3","recommendedVersion":"0.0.5","nextCheckInSeconds":60}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: "v0.0.5"}
	var queue V4Queue
	queue.FirstSync.HistoryState = "complete"
	if result, err := client.V4CheckIn(context.Background(), queue, 0, nil, nil, nil); err != nil || result.ConfigVersion != 0 || result.Config.LeaveOut == nil {
		t.Fatalf("default policy check-in=%+v err=%v", result, err)
	}
}

// The sync log goes out in the contract's DeviceLogInput shape, and a
// check-in with nothing to report omits the field.
func TestV4CheckInSendsTheSyncLogInTheContractShape(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		io.WriteString(w, `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.3"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: "v0.0.5"}
	var queue V4Queue
	queue.FirstSync.HistoryState = "complete"
	at := time.Date(2026, 9, 29, 12, 0, 0, 123456000, time.UTC)
	log := []V4LogEntry{{At: at, SessionID: "ses_one", Level: "error", Message: "The space is full.", Code: "space_full"},
		{At: at, Level: "error", Message: "coSlash Local cannot read the session folder.", Code: "unreadable_source"}}
	if _, err := client.V4CheckIn(context.Background(), queue, 1, nil, nil, log); err != nil {
		t.Fatal(err)
	}
	if _, err := client.V4CheckIn(context.Background(), queue, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Log []map[string]any `json:"log"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &sent); err != nil {
		t.Fatal(err)
	}
	want := `[{"at":"2026-09-29T12:00:00.123456Z","code":"space_full","level":"error","message":"The space is full.","sessionId":"ses_one"},` +
		`{"at":"2026-09-29T12:00:00.123456Z","code":"unreadable_source","level":"error","message":"coSlash Local cannot read the session folder."}]`
	if got, _ := json.Marshal(sent.Log); string(got) != want {
		t.Fatalf("log=%s", got)
	}
	if strings.Contains(bodies[1], `"log"`) {
		t.Fatalf("empty log was sent: %s", bodies[1])
	}
}

func TestV4WaitCarriesSinceAndDeviceCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v4/devices/me/wait" || r.URL.Query().Get("since") != "8" || r.Header.Get("Authorization") != "Device credential" {
			t.Errorf("wait request=%s %s auth=%q", r.Method, r.URL.String(), r.Header.Get("Authorization"))
		}
		io.WriteString(w, `{"configVersion":9,"changed":true,"commandsAvailable":false,"syncRequested":true}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}}
	result, err := client.V4Wait(context.Background(), 8)
	if err != nil || !result.Changed || !result.SyncRequested || result.ConfigVersion != 9 {
		t.Fatalf("wait=%+v err=%v", result, err)
	}
}

func TestV4CheckInRejectsIncompletePolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"configVersion":8,"config":{"paused":false,"deviceOff":false,"agentKnowledge":true},"commands":[],"minVersion":"0.0.3"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: "v0.0.5"}
	var queue V4Queue
	queue.FirstSync.HistoryState = "complete"
	if _, err := client.V4CheckIn(context.Background(), queue, 7, nil, nil, nil); err == nil {
		t.Fatal("missing leave-out policy was accepted")
	}
}

func TestV4PutChunkFallsBackToAuthenticatedProxy(t *testing.T) {
	const body = "verified chunk"
	var proxyCalls int
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer direct.Close()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/uploads/upload/chunks:sign":
			io.WriteString(w, `{"urls":[{"artifactOrdinal":0,"chunkOrdinal":0,"url":"`+direct.URL+`/signed","headers":{},"expiresAt":"2026-09-28T12:00:00Z"}]}`)
		case "/v4/uploads/upload/chunks/0/0":
			proxyCalls++
			if r.Method != http.MethodPut || r.Header.Get("Authorization") != "Device credential" || r.ContentLength != int64(len(body)) {
				t.Fatalf("proxy request = %s auth=%q length=%d", r.Method, r.Header.Get("Authorization"), r.ContentLength)
			}
			got, err := io.ReadAll(r.Body)
			if err != nil || string(got) != body {
				t.Fatalf("proxy body = %q, err=%v", got, err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer hub.Close()
	base, _ := url.Parse(hub.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}}
	if err := client.V4PutChunk(context.Background(), "upload", V4Missing{ArtifactOrdinal: 0, ChunkOrdinal: 0, Bytes: int64(len(body))}, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if proxyCalls != 1 {
		t.Fatalf("proxy calls = %d; want 1", proxyCalls)
	}
}

func TestV4ConfirmSendsOneBatchOfUpToFiftyChunks(t *testing.T) {
	var requests, coords int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v4/uploads/upload/chunks:confirm" || r.Header.Get("Authorization") != "Device credential" {
			t.Fatalf("confirm request = %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var input struct {
			Coords []struct {
				ArtifactOrdinal int `json:"artifactOrdinal"`
				ChunkOrdinal    int `json:"chunkOrdinal"`
			} `json:"coords"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		for index, coord := range input.Coords {
			if coord.ArtifactOrdinal != index || coord.ChunkOrdinal != index%2 {
				t.Fatalf("coord %d = %+v", index, coord)
			}
		}
		requests, coords = requests+1, coords+len(input.Coords)
		io.WriteString(w, `{"uploadId":"upload","sessionId":"ses_1","state":"open","missing":[]}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}}
	batch := make([]V4Missing, V4MaxConfirm+1)
	for index := range batch {
		batch[index] = V4Missing{ArtifactOrdinal: index, ChunkOrdinal: index % 2, Bytes: 1}
	}
	status, err := client.V4Confirm(context.Background(), "upload", batch[:V4MaxConfirm]...)
	if err != nil || status.State != "open" || requests != 1 || coords != V4MaxConfirm {
		t.Fatalf("confirm status=%+v err=%v requests=%d coords=%d", status, err, requests, coords)
	}
	for _, invalid := range [][]V4Missing{nil, batch} {
		if _, err := client.V4Confirm(context.Background(), "upload", invalid...); err == nil {
			t.Fatalf("confirm of %d chunks was sent", len(invalid))
		}
	}
	if requests != 1 {
		t.Fatalf("invalid batches reached the Hub: %d requests", requests)
	}
}
