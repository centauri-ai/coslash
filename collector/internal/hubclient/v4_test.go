package hubclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v4/devices/me/check-in" || r.Header.Get("Authorization") != "Device credential" {
			t.Fatalf("check-in request = %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var input struct {
			OS                   string   `json:"os"`
			AppliedConfigVersion int64    `json:"appliedConfigVersion"`
			Capabilities         []string `json:"capabilities"`
			Queue                V4Queue  `json:"queue"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.OS != runtime.GOOS || input.AppliedConfigVersion != 7 || len(input.Capabilities) != 2 ||
			input.Queue.FirstSync.RecentDone != 2 || input.Queue.FirstSync.RecentTotal != 3 || input.Queue.FirstSync.HistoryState != "syncing" {
			t.Fatalf("check-in input = %+v", input)
		}
		io.WriteString(w, `{"configVersion":8,"config":{"paused":false,"deviceOff":false,"leaveOut":[]},"commands":[],"minVersion":"0.0.5","recommendedVersion":"0.0.5","nextCheckInSeconds":300}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: "v0.0.5"}
	var queue V4Queue
	queue.Pending = 1
	queue.FirstSync.RecentDone, queue.FirstSync.RecentTotal, queue.FirstSync.HistoryState = 2, 3, "syncing"
	result, err := client.V4CheckIn(context.Background(), queue, 7)
	if err != nil || result.ConfigVersion != 8 {
		t.Fatalf("check-in result=%+v err=%v", result, err)
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
