package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestManagerTracksBackgroundReviewFailureAndRetry(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan error, 2)
	manager := NewManager(func(context.Context, Launch) error {
		started <- struct{}{}
		return <-release
	})
	request := Launch{Reviewer: "codex", WorkingDirectory: "/repo", Name: "review", Prompt: "prompt"}

	if !manager.Start("origin", request) {
		t.Fatal("Start() = false")
	}
	<-started
	if state := manager.Status("origin"); !state.Pending || state.Error != "" {
		t.Fatalf("pending state = %#v", state)
	}
	if manager.Start("origin", request) {
		t.Fatal("duplicate Start() = true")
	}

	release <- errors.New("agent failed")
	waitForReviewState(t, manager, "origin", func(state State) bool { return state.Error == failureMessage })
	if !manager.Start("origin", request) {
		t.Fatal("retry Start() = false")
	}
	<-started
	release <- nil
	waitForReviewState(t, manager, "origin", func(state State) bool { return !state.Pending && state.Error == "" })
}

func TestManagerShutdownCancelsRunningReview(t *testing.T) {
	started := make(chan struct{})
	manager := NewManager(func(ctx context.Context, _ Launch) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	manager.Start("origin", Launch{})
	<-started

	manager.Shutdown()

	if state := manager.Status("origin"); state.Pending {
		t.Fatalf("state after shutdown = %#v", state)
	}
}

func waitForReviewState(t *testing.T, manager *Manager, id string, done func(State) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if state := manager.Status(id); done(state) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for review state: %#v", manager.Status(id))
}

func TestNameUsesOriginNameAndEightCharacterID(t *testing.T) {
	got := Name("Fix checkout race", "12345678-aaaa-bbbb-cccc-123456789abc")
	if got != "Review — Fix checkout race (12345678)" {
		t.Fatalf("Name() = %q", got)
	}
}

func TestNameFallsBackForUntitledSession(t *testing.T) {
	got := Name("", "ses_123456789")
	if got != "Review — Untitled session (ses_1234)" {
		t.Fatalf("Name() = %q", got)
	}
}

func TestNameNormalizesMultilineOriginName(t *testing.T) {
	got := Name("Fix checkout\nwith a second line", "12345678-rest")
	if got != "Review — Fix checkout with a second line (12345678)" {
		t.Fatalf("Name() = %q", got)
	}
}

func TestPromptCarriesNameAndBoundedReviewContext(t *testing.T) {
	name := "Fix checkout race"
	branch := "feature/checkout"
	outcome := "Implemented checkout locking"
	edits := session.NewFileEditSet()
	edits.Add("checkout.go", 1, 1, false)
	edits.Patch("checkout.go", "@@\n-old\n+new")
	origin := &session.Session{
		ID:     "12345678-rest",
		Name:   &name,
		Branch: &branch,
		SessionDetails: session.SessionDetails{
			Synthesis:  &session.SessionSynthesis{Outcome: outcome},
			FileEdits:  edits.Edits,
			Commits:    []string{"abc123 Fix checkout"},
			CommitSHAs: []string{"abc123def456"},
		},
	}

	prompt := Prompt(origin)
	parsed, ok := NameFromPrompt(prompt)
	if !ok || parsed != "Review — Fix checkout race (12345678)" {
		t.Fatalf("NameFromPrompt() = %q, %v", parsed, ok)
	}
	for _, want := range []string{
		"Review the current working-tree changes",
		"Do not modify files",
		"feature/checkout",
		"Implemented checkout locking",
		"checkout.go",
		"-old",
		"+new",
		"abc123 Fix checkout",
		"abc123def456",
		"BEGIN UNTRUSTED SESSION DATA",
		"END UNTRUSTED SESSION DATA",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("Prompt() missing %q", want)
		}
	}
}

func TestPromptIsBounded(t *testing.T) {
	name := strings.Repeat("name ", 100)
	branch := strings.Repeat("branch ", 100)
	outcome := strings.Repeat("outcome ", 2_000)
	origin := &session.Session{ID: "12345678-rest", Name: &name, Branch: &branch, Summary: &outcome}
	for range maxFiles + 1 {
		origin.FileEdits = append(origin.FileEdits, session.FileEdit{Path: strings.Repeat("path/", 200)})
	}
	for range maxCommits + 1 {
		origin.Commits = append(origin.Commits, strings.Repeat("commit ", 200))
	}

	prompt := Prompt(origin)
	if len(prompt) > maxPromptBytes {
		t.Fatalf("Prompt() length = %d", len(prompt))
	}
	if _, ok := NameFromPrompt(prompt); !ok {
		t.Fatalf("Prompt() lost review name: %q", prompt[:200])
	}
}

func TestNameFromPromptRejectsOrdinaryPrompt(t *testing.T) {
	for _, prompt := range []string{"Review this code", "Review — Something (short)"} {
		if name, ok := NameFromPrompt(prompt); ok || name != "" {
			t.Fatalf("NameFromPrompt(%q) = %q, %v", prompt, name, ok)
		}
	}
}
