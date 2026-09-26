package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/directedhandoff"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func TestDirectedHandoffRejectsEmptyCustomRequestAndPersistsLaunchFailure(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "handoffs.json")
	store, err := directedhandoff.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	previousTargets, previousSession, previousTerminal := directedLocalTargets, directedLocalSession, directedLocalTerminal
	t.Cleanup(func() {
		directedLocalTargets, directedLocalSession, directedLocalTerminal = previousTargets, previousSession, previousTerminal
	})
	directedLocalTargets = func(context.Context) []launch.HandoffTargetOption {
		return []launch.HandoffTargetOption{{Agent: "claude", Label: "Claude Code", Available: true, Automatic: true}}
	}
	directedLocalSession = func(agent, id string, revision int64) (*session.Session, error) {
		return &session.Session{Agent: agent, ID: id, WorkingDirectory: t.TempDir()}, nil
	}
	launched := false
	directedLocalTerminal = func(context.Context, string, string, string, string, string, string, string) error {
		launched = true
		return errors.New("test launch failure")
	}
	settingsStore := settings.Open()
	remoteManager := remote.NewManager(remote.Options{})
	synthesisManager := synthesis.NewManager(nil)
	for _, test := range []struct {
		request string
		status  int
		count   int
	}{
		{request: "", status: http.StatusBadRequest, count: 0},
		{request: "  ", status: http.StatusBadRequest, count: 0},
		{request: "Please continue", status: http.StatusInternalServerError, count: 1},
	} {
		body := `{"sourceId":"local","agent":"codex","id":"origin","targetAgent":"claude","kind":"custom","request":"` + test.request + `"}`
		response := httptest.NewRecorder()
		handleDirectedHandoffStart(response, httptest.NewRequest(http.MethodPost, "/api/directed-handoffs", strings.NewReader(body)), store, settingsStore, remoteManager, synthesisManager)
		if response.Code != test.status || len(store.List()) != test.count {
			t.Fatalf("request %q: code %d, records %#v", test.request, response.Code, store.List())
		}
	}
	if !launched {
		t.Fatal("valid request did not launch")
	}
	if got := store.List()[0]; got.Status != "failed" {
		t.Fatalf("launch failure = %#v", got)
	}
	restored, err := directedhandoff.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.List(); len(got) != 1 || got[0].Status != "failed" {
		t.Fatalf("persisted failure = %#v", got)
	}
}

func TestRemoteDirectedTerminalStagesOnlyBrief(t *testing.T) {
	previousStage, previousLaunch, previousRemove := stageRemoteHandoff, launchDirectedRemoteTerminal, removeRemoteHandoff
	t.Cleanup(func() {
		stageRemoteHandoff, launchDirectedRemoteTerminal, removeRemoteHandoff = previousStage, previousLaunch, previousRemove
	})
	stages := 0
	stageRemoteHandoff = func(_ context.Context, alias string, content []byte) (string, error) {
		stages++
		if alias != "host" || !strings.Contains(string(content), "brief") {
			t.Fatalf("stage %q %q", alias, content)
		}
		return "brief-name", nil
	}
	launchDirectedRemoteTerminal = func(_ context.Context, _, alias, agent, cwd, _, mode, briefName, prompt string) error {
		if alias != "host" || agent != "claude" || cwd != "/workspace" || mode != launch.NewSession || briefName != "brief-name" || prompt != "marker and request" {
			t.Fatalf("launch alias=%q agent=%q cwd=%q mode=%q brief=%q prompt=%q", alias, agent, cwd, mode, briefName, prompt)
		}
		return errors.New("launch failed")
	}
	removed := ""
	removeRemoteHandoff = func(_ context.Context, alias, name string) error { removed = name; return nil }
	if err := openRemoteDirectedTerminal(context.Background(), "terminal", "host", "claude", "/workspace", "brief", "marker and request"); err == nil {
		t.Fatal("expected launch failure")
	}
	if stages != 1 || removed != "brief-name" {
		t.Fatalf("stages=%d removed=%q", stages, removed)
	}
}
