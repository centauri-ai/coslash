package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/centauri-ai/coslash/collector/internal/directedhandoff"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPiDirectedHandoffCustomLocalOnly(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	store, err := directedhandoff.Open(filepath.Join(t.TempDir(), "handoffs.json"))
	if err != nil {
		t.Fatal(err)
	}
	targets, source, open := directedLocalTargets, directedLocalSession, directedLocalTerminal
	t.Cleanup(func() { directedLocalTargets, directedLocalSession, directedLocalTerminal = targets, source, open })
	directedLocalTargets = func(context.Context) []launch.HandoffTargetOption {
		return []launch.HandoffTargetOption{{Agent: "pi", Available: true, Automatic: true}}
	}
	directedLocalSession = func(agent, id string, _ int64) (*session.Session, error) {
		return &session.Session{Agent: agent, ID: id, WorkingDirectory: t.TempDir()}, nil
	}
	launched := 0
	directedLocalTerminal = func(_ context.Context, _, agent, _, _, mode, _, prompt string) error {
		launched++
		if agent != "pi" || mode != launch.NewSession || !strings.Contains(prompt, "Please continue") {
			t.Fatal("wrong launch")
		}
		return nil
	}
	for _, test := range []struct {
		source, kind string
		status       int
	}{{"remote", "review", 400}, {"remote", "custom", 400}, {"local", "custom", 202}} {
		body := `{"sourceId":"` + test.source + `","agent":"codex","id":"origin","targetAgent":"pi","kind":"` + test.kind + `","request":"Please continue"}`
		response := httptest.NewRecorder()
		handleDirectedHandoffStart(response, httptest.NewRequest(http.MethodPost, "/api/directed-handoffs", strings.NewReader(body)), store, settings.Open(), remote.NewManager(remote.Options{}), synthesis.NewManager(nil, nil))
		if response.Code != test.status {
			t.Fatalf("%+v: %d %s", test, response.Code, response.Body.String())
		}
	}
	if launched != 1 {
		t.Fatalf("launch count %d", launched)
	}
}

func TestPiDirectedReviewIndependentOfTerminalSupport(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("Pi reviews require macOS or Windows")
	}
	t.Setenv("COSLASH_HOME", t.TempDir())
	store, err := directedhandoff.Open(filepath.Join(t.TempDir(), "handoffs.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Shutdown()
	available, run, source, targets := directedLocalReviewerAvailable, directedLocalReview, directedLocalSession, directedLocalTargets
	t.Cleanup(func() {
		directedLocalReviewerAvailable, directedLocalReview, directedLocalSession, directedLocalTargets = available, run, source, targets
	})
	directedLocalReviewerAvailable = func(_ context.Context, agent string) bool { return agent == "pi" }
	directedLocalTargets = func(context.Context) []launch.HandoffTargetOption { return nil }
	cwd := t.TempDir()
	directedLocalSession = func(agent, id string, _ int64) (*session.Session, error) {
		return &session.Session{Agent: agent, ID: id, WorkingDirectory: cwd}, nil
	}
	started := make(chan review.Launch, 1)
	directedLocalReview = func(_ context.Context, request review.Launch) (string, error) {
		started <- request
		return "Found a regression", nil
	}
	settingsStore := settings.Open()
	for _, test := range []struct {
		kind   string
		wantPi bool
	}{{"review", true}, {"custom", false}} {
		response := httptest.NewRecorder()
		handleDirectedHandoffTargets(response, httptest.NewRequest(http.MethodGet, "/api/directed-handoffs/targets?source=local&kind="+test.kind, nil), settingsStore)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"id":"pi"`) != test.wantPi {
			t.Fatalf("%s targets: %d %s", test.kind, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	body := `{"sourceId":"local","agent":"codex","id":"origin","targetAgent":"pi","kind":"review"}`
	handleDirectedHandoffStart(response, httptest.NewRequest(http.MethodPost, "/api/directed-handoffs", strings.NewReader(body)), store, settingsStore, remote.NewManager(remote.Options{}), synthesis.NewManager(nil, nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("Pi review: %d %s", response.Code, response.Body.String())
	}
	store.Shutdown()
	request := <-started
	if request.Reviewer != "pi" || request.WorkingDirectory != cwd || !strings.Contains(request.Prompt, "<session-context>") {
		t.Fatalf("Pi launch = %#v", request)
	}
	if records := store.List(); len(records) != 1 || records[0].Status != "completed" || records[0].Result != "Found a regression" {
		t.Fatalf("Pi review result = %#v", records)
	}
}

func TestTerminalLaunchErrorsUseStructuredContract(t *testing.T) {
	for _, test := range []struct {
		err           error
		status        int
		code, message string
	}{
		{fmt.Errorf("wrapped: %w", launch.ErrPiUnsupportedVersion), 409, "pi_runtime_unsupported", "Pi launch requires a stable release at least 0.99.1"},
		{launch.ErrPiExtension, 409, "pi_extension_unavailable", "Required Pi extension is unavailable; check local setup."},
		{launch.ErrWorkingDirectoryUnavailable, 409, "working_directory_unavailable", "Session working directory is unavailable."},
		{errors.New("private process details"), 500, "terminal_launch_failed", "Could not launch terminal."},
	} {
		response := httptest.NewRecorder()
		writeTerminalLaunchError(response, test.err)
		var body struct{ Code, Error string }
		if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Code != test.code || body.Error != test.message {
			t.Fatalf("%v: %d %s", test.err, response.Code, response.Body.String())
		}
		if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
			t.Fatal("launch error is not JSON")
		}
	}
}
func TestPiRemoteLaunchRejectedBeforeLookup(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	response := httptest.NewRecorder()
	handleLaunch(response, httptest.NewRequest(http.MethodPost, "/api/launch?source=remote&agent=pi&id=opaque&mode=new", nil), settings.Open(), remote.NewManager(remote.Options{}))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}
