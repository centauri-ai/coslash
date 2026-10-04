package main

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

type shareFixtureRunner struct {
	run func(context.Context, string) (session.SessionSynthesis, error)
}

func (r shareFixtureRunner) Run(ctx context.Context, input string) (session.SessionSynthesis, error) {
	return r.run(ctx, input)
}

func (shareFixtureRunner) ModelName() string { return "synthetic-model" }

func TestShareSynthesisReadinessGatesBackendAndRevision(t *testing.T) {
	found := &session.Session{Agent: "codex", ID: "synthetic", LastActivityTime: 123,
		SessionDetails: session.SessionDetails{Turns: 6}}
	state := settings.State{Config: settings.Defaults(), Valid: true, Persisted: true}
	state.Config.Synthesis.Enabled = true
	for _, test := range []struct {
		name     string
		revision int64
		mutate   func(*session.Session, *settings.State)
		want     string
	}{
		{"source changed", 122, nil, "revision_changed"},
		{"ineligible", 123, func(s *session.Session, _ *settings.State) { s.Turns = 1 }, "ineligible"},
		{"consent missing", 123, func(_ *session.Session, state *settings.State) { state.Persisted = false }, "consent_required"},
		{"disabled", 123, func(_ *session.Session, state *settings.State) { state.Config.Synthesis.Enabled = false }, "disabled"},
		{"backend unavailable", 123, nil, "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			copySession, copyState := *found, state
			if test.mutate != nil {
				test.mutate(&copySession, &copyState)
			}
			got := shareSynthesisReadiness(&copySession, test.revision, synthesis.NewManager(nil), copyState)
			if got.State != test.want || got.Synthesis != nil || got.Revision != 123 {
				t.Fatalf("readiness = %+v, want %s", got, test.want)
			}
		})
	}
}

func TestShareSynthesisReadinessReusesPersistedRevision(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	want := session.SessionSynthesis{Goals: []string{"First", "Second"}, Outcome: "Done",
		KeyDecisions: []string{"Keep order"}, NextStep: "Measure"}
	if err := synthesis.NewCache().Store("codex", "synthetic", synthesis.Record{
		Revision: 123, Model: "synthetic-model", GeneratedAt: 456, Synthesis: want,
	}); err != nil {
		t.Fatal(err)
	}
	found := &session.Session{Agent: "codex", ID: "synthetic", LastActivityTime: 123,
		SessionDetails: session.SessionDetails{Turns: 6}}
	state := settings.State{Config: settings.Defaults(), Valid: true, Persisted: true}
	got := shareSynthesisReadiness(found, 123, synthesis.NewManager(nil), state)
	if got.State != "ready" || got.GeneratedAt != 456 || got.Model != "synthetic-model" ||
		got.Synthesis == nil || !reflect.DeepEqual(*got.Synthesis, want) {
		t.Fatalf("persisted readiness = %+v", got)
	}
	found.LastActivityTime = 124
	if stale := shareSynthesisReadiness(found, 124, synthesis.NewManager(nil), state); stale.State == "ready" {
		t.Fatalf("stale synthesis reused: %+v", stale)
	}
}

func TestShareSynthesisReadinessRejectsMissingConfiguredCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	found := &session.Session{Agent: "codex", ID: "synthetic", LastActivityTime: 123,
		SessionDetails: session.SessionDetails{Turns: 6}}
	state := settings.State{Config: settings.Defaults(), Valid: true, Persisted: true}
	state.Config.Synthesis.Enabled = true
	mgr := synthesis.NewManager(&synthesis.CLIRunner{Bin: "synthetic-missing-cli"})
	if got := shareSynthesisReadiness(found, 123, mgr, state); got.State != "unavailable" || mgr.Running("codex", "synthetic") {
		t.Fatalf("missing CLI readiness = %+v", got)
	}
}

func TestShareSynthesisReadinessGeneratesOnceAndWaitsForPersistedRecord(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	want := session.SessionSynthesis{Goals: []string{"First", "Second"}, Outcome: "Done",
		KeyDecisions: []string{"Keep order"}, NextStep: "Measure"}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	mgr := synthesis.NewManager(shareFixtureRunner{run: func(context.Context, string) (session.SessionSynthesis, error) {
		calls.Add(1)
		close(started)
		<-release
		return want, nil
	}})
	found := &session.Session{Agent: "codex", ID: "synthetic", LastActivityTime: 123,
		SessionDetails: session.SessionDetails{Turns: 6}}
	state := settings.State{Config: settings.Defaults(), Valid: true, Persisted: true}
	state.Config.Synthesis.Enabled = true
	for index := 0; index < 2; index++ {
		if got := shareSynthesisReadiness(found, 123, mgr, state); got.State != "pending" || got.Synthesis != nil {
			t.Fatalf("readiness before persistence = %+v", got)
		}
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic runner did not start")
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent generation calls = %d", calls.Load())
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := shareSynthesisReadiness(found, 123, mgr, state)
		if got.State == "ready" {
			if got.GeneratedAt <= 0 || got.Model != "synthetic-model" || got.Synthesis == nil ||
				!reflect.DeepEqual(*got.Synthesis, want) || calls.Load() != 1 {
				t.Fatalf("persisted result = %+v, calls = %d", got, calls.Load())
			}
			return
		}
		if got.State != "pending" {
			t.Fatalf("readiness while waiting = %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("synthetic synthesis did not persist")
}

func TestShareSynthesisReadinessReportsFailureWithoutRetrying(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	var calls atomic.Int32
	mgr := synthesis.NewManager(shareFixtureRunner{run: func(context.Context, string) (session.SessionSynthesis, error) {
		calls.Add(1)
		return session.SessionSynthesis{}, errors.New("synthetic private provider output")
	}})
	found := &session.Session{Agent: "codex", ID: "synthetic", LastActivityTime: 123,
		SessionDetails: session.SessionDetails{Turns: 6}}
	state := settings.State{Config: settings.Defaults(), Valid: true, Persisted: true}
	state.Config.Synthesis.Enabled = true
	if got := shareSynthesisReadiness(found, 123, mgr, state); got.State != "pending" {
		t.Fatalf("initial readiness = %+v", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := shareSynthesisReadiness(found, 123, mgr, state)
		if got.State == "failed" {
			if got.Synthesis != nil || calls.Load() != 1 {
				t.Fatalf("failed readiness = %+v, calls = %d", got, calls.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("synthetic failure was not reported")
}
