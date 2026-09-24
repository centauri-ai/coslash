package handoff

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestBuildRendersCanonicalMarkdown(t *testing.T) {
	name, goal, summary, branch, repo := "Fix auth", "Ship the fix", "Tests pass", "feature/auth", "centauri/coslash"
	duration, cost := 90_000, 1.25
	value := &session.Session{
		Agent: "codex", ID: "session-1", Name: &name, Summary: &summary,
		WorkingDirectory: "/workspace", Branch: &branch, Repository: &repo,
		DurationMs: &duration, Cost: &cost,
		Tokens: map[string]session.ModelTokens{"gpt-5": {InputTokens: 1200, OutputTokens: 300}},
		SessionDetails: session.SessionDetails{
			DeclaredGoal: &goal, Errors: 1,
			Todos:     []session.Todo{{Text: "Done", Done: true}, {Text: "Open PR", Done: false}},
			Commits:   []string{"abc123 Fix auth"},
			FileEdits: []session.FileEdit{{Path: "auth.go", Additions: 4, Deletions: 1}},
			Digest:    []session.DigestEntry{{Turn: 2, Category: session.DigestUser, Description: "Asked for tests"}},
			Synthesis: &session.SessionSynthesis{Outcome: "Tests pass", KeyDecisions: []string{"Reuse the guard"}},
		},
	}

	got := Build(value)
	for _, want := range []string{
		"# Handoff — Fix auth",
		"## Objective (declared)\nShip the fix",
		"## Current state\nTests pass",
		"- Reuse the guard",
		"- [user · turn 2] Asked for tests",
		"- auth.go (+4/-1)",
		"- abc123 Fix auth",
		"## Next steps\n- Open PR",
		"- Vendor: Codex",
		"- Repository: centauri/coslash",
		"- Runtime: 2m",
		"- Tokens: 2k",
		"- Estimated cost at list API prices: ≈$1.25",
		"- Errors: 1; subagents: 0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("handoff missing %q:\n%s", want, got)
		}
	}
}

func TestBuildUsesStableFallbacks(t *testing.T) {
	got := Build(&session.Session{Agent: "claude", ID: "session-1"})
	for _, want := range []string{
		"# Handoff — session-1",
		"## Objective (first prompt)\n—",
		"- Repository: —",
		"- Branch: —",
		"- Working directory: —",
		"- Runtime: —",
		"- Tokens: —",
		"- Estimated cost at list API prices: —",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("handoff missing %q:\n%s", want, got)
		}
	}
}

func TestBuildRendersQuestionAnswers(t *testing.T) {
	got := Build(&session.Session{ID: "session-1", SessionDetails: session.SessionDetails{
		Digest: []session.DigestEntry{{
			Turn: 3, Category: session.DigestQuestion, Description: "Which database?", Answer: "Postgres",
		}},
	}})
	if !strings.Contains(got, "- [question · turn 3] Which database?\n  - Answer: Postgres") {
		t.Fatalf("handoff omitted the recorded answer:\n%s", got)
	}
}

func TestBuildKeepsSummaryWhenSynthesisOutcomeIsBlank(t *testing.T) {
	summary := "Tests pass"
	got := Build(&session.Session{
		ID: "session-1", Summary: &summary,
		SessionDetails: session.SessionDetails{Synthesis: &session.SessionSynthesis{Goals: []string{"Ship it"}}},
	})
	if !strings.Contains(got, "## Current state\nTests pass") {
		t.Fatalf("handoff discarded the summary:\n%s", got)
	}
}

func TestBuildOmitsUnavailableCursorSections(t *testing.T) {
	prompt := "Investigate the issue"
	got := Build(&session.Session{Agent: "cursor", ID: "cursor-1", SessionDetails: session.SessionDetails{
		FirstPrompt: &prompt,
		Digest:      []session.DigestEntry{{Turn: 1, Category: session.DigestFirstPrompt, Description: prompt}},
	}})
	for _, absent := range []string{"## Key decisions", "## Files", "## Commits", "## Next steps"} {
		if strings.Contains(got, absent) {
			t.Fatalf("handoff includes empty %q:\n%s", absent, got)
		}
	}
	for _, present := range []string{"## Objective (first prompt)", "## Timeline", "## Environment"} {
		if !strings.Contains(got, present) {
			t.Fatalf("handoff missing %q:\n%s", present, got)
		}
	}
}
