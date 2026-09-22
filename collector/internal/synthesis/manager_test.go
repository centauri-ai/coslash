package synthesis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestManagerRunSkipsListUntilRunnerConfigured(t *testing.T) {
	manager := NewManager(nil)
	listCalls := 0
	list := func() ([]*session.Session, error) {
		listCalls++
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manager.Run(ctx, list)
	if listCalls != 0 {
		t.Fatalf("list called %d times without a runner", listCalls)
	}

	manager.SetRunner(runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		return session.SessionSynthesis{}, nil
	}))
	manager.sweep(list)
	if listCalls != 1 {
		t.Fatalf("list called %d times after configuring a runner", listCalls)
	}
}

func overflowSession() *session.Session {
	return overflowSessionWithTurns(30)
}

func overflowSessionWithTurns(turns int) *session.Session {
	digest := make([]session.DigestEntry, turns)
	for index := range digest {
		digest[index] = session.DigestEntry{
			Turn:        index + 1,
			Category:    session.DigestRecap,
			Description: fmt.Sprintf("history-%02d %s", index+1, strings.Repeat("x", 500)),
		}
	}
	return &session.Session{ID: "long", Agent: "codex", SessionDetails: session.SessionDetails{Digest: digest}}
}

func TestRunSynthesisMergesEveryOverflowChunk(t *testing.T) {
	var chunks []string
	runner := runnerFunc(func(_ context.Context, input string) (session.SessionSynthesis, error) {
		if strings.Contains(input, "PARTIAL SYNTHESES") {
			for _, chunk := range chunks {
				if !strings.Contains(input, chunk) {
					t.Fatalf("merge input omitted %q", chunk)
				}
			}
			return session.SessionSynthesis{Outcome: "merged history"}, nil
		}
		marker := fmt.Sprintf("chunk-%d", len(chunks)+1)
		chunks = append(chunks, marker)
		return session.SessionSynthesis{Outcome: marker}, nil
	})

	got, err := runSynthesis(context.Background(), runner, overflowSession())
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("runner saw %d source chunk, want overflow", len(chunks))
	}
	if got.Outcome != "merged history" {
		t.Fatalf("runSynthesis() = %#v", got)
	}
}

func TestRunSynthesisStopsOnChunkFailure(t *testing.T) {
	want := errors.New("chunk failed")
	calls := 0
	runner := runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		calls++
		if calls == 2 {
			return session.SessionSynthesis{}, want
		}
		return session.SessionSynthesis{Outcome: "partial"}, nil
	})

	_, err := runSynthesis(context.Background(), runner, overflowSession())
	if !errors.Is(err, want) {
		t.Fatalf("runSynthesis() error = %v, want %v", err, want)
	}
	if calls != 2 {
		t.Fatalf("runner called %d times after failure, want 2", calls)
	}
}

func TestRunSynthesisRejectsExcessiveWorkBeforeCallingRunner(t *testing.T) {
	digest := make([]session.DigestEntry, 400)
	for index := range digest {
		digest[index] = session.DigestEntry{
			Turn:        index + 1,
			Category:    session.DigestRecap,
			Description: strings.Repeat("界", 500),
		}
	}
	calls := 0
	runner := runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		calls++
		return session.SessionSynthesis{Outcome: "partial"}, nil
	})

	_, err := runSynthesis(context.Background(), runner, &session.Session{SessionDetails: session.SessionDetails{Digest: digest}})
	if err == nil || !strings.Contains(err.Error(), "work limit") {
		t.Fatalf("runSynthesis() error = %v, want work limit", err)
	}
	if calls != 0 {
		t.Fatalf("runner called %d times before rejecting excessive work", calls)
	}
}

func TestRunSynthesisKeepsBoundedContextWithinSourceRunLimit(t *testing.T) {
	goal := "goal-marker " + strings.Repeat("g", 988)
	repository := strings.Repeat("r", 300)
	branch := strings.Repeat("b", 300)
	digest := make([]session.DigestEntry, 100)
	for index := range digest {
		digest[index] = session.DigestEntry{
			Turn:        index + 1,
			Category:    session.DigestRecap,
			Description: fmt.Sprintf("history-%03d %s", index, strings.Repeat("d", 488)),
		}
	}
	edits := make([]session.FileEdit, 30)
	for index := range edits {
		edits[index].Path = strings.Repeat("artifact", 63)
	}
	s := &session.Session{
		ID:               strings.Repeat("i", 200),
		Agent:            strings.Repeat("a", 100),
		Repository:       &repository,
		Branch:           &branch,
		WorkingDirectory: strings.Repeat("w", 500),
		SessionDetails: session.SessionDetails{
			DeclaredGoal:   &goal,
			FirstPrompt:    &goal,
			CompactionSeed: "seed-marker " + strings.Repeat("s", 3_988),
			Digest:         digest,
			FileEdits:      edits,
		},
	}
	calls := 0
	var inputs []string
	runner := runnerFunc(func(_ context.Context, input string) (session.SessionSynthesis, error) {
		calls++
		inputs = append(inputs, input)
		return session.SessionSynthesis{Outcome: "partial"}, nil
	})

	if _, err := runSynthesis(context.Background(), runner, s); err != nil {
		t.Fatalf("runSynthesis() error = %v, want bounded synthesis", err)
	}
	if calls == 0 {
		t.Fatal("runner was not called")
	}
	joined := strings.Join(inputs, "\n")
	for _, marker := range []string{"goal-marker", "seed-marker", "history-000", "history-099"} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("synthesis inputs omitted %q", marker)
		}
	}
}

func TestRunSynthesisRejectsImpossibleDigestBeforeBuildingPrompts(t *testing.T) {
	calls := 0
	runner := runnerFunc(func(context.Context, string) (session.SessionSynthesis, error) {
		calls++
		return session.SessionSynthesis{}, nil
	})

	_, err := runSynthesis(context.Background(), runner, &session.Session{SessionDetails: session.SessionDetails{
		Digest: make([]session.DigestEntry, 27_429),
	}})
	if err == nil || !strings.Contains(err.Error(), "digest item limit") {
		t.Fatalf("runSynthesis() error = %v, want digest item limit", err)
	}
	if calls != 0 {
		t.Fatalf("runner called %d times before rejecting impossible digest", calls)
	}
}

func TestRunSynthesisCarriesEarlyAndLateDecisionsAcrossMergeRounds(t *testing.T) {
	s := overflowSessionWithTurns(120)
	sourceCount := len(BuildInputs(s))
	if sourceCount < 3 {
		t.Fatalf("test session produced %d source chunks, want at least 3", sourceCount)
	}
	sourceCalls := 0
	mergeCalls := 0
	sawCombinedMarkers := false
	runner := runnerFunc(func(_ context.Context, input string) (session.SessionSynthesis, error) {
		if !strings.Contains(input, "PARTIAL SYNTHESES") {
			index := sourceCalls
			sourceCalls++
			decisions := make([]string, maxIntermediateKeyDecisions)
			for decisionIndex := range decisions {
				decisions[decisionIndex] = fmt.Sprintf("source-%02d-%02d", index, decisionIndex)
			}
			if index == 0 {
				decisions[0] = "early-decision-marker"
			}
			if index == sourceCount-1 {
				decisions[len(decisions)-1] = "late-decision-marker"
			}
			return session.SessionSynthesis{Outcome: strings.Repeat("o", 2_000), KeyDecisions: decisions}, nil
		}

		mergeCalls++
		var decisions []string
		for _, line := range strings.Split(input, "\n") {
			if decision, ok := strings.CutPrefix(line, "Decision: "); ok {
				decisions = append(decisions, decision)
			}
		}
		if strings.Contains(input, "early-decision-marker") && strings.Contains(input, "late-decision-marker") {
			sawCombinedMarkers = true
		}
		ranked := make([]string, 0, len(decisions))
		for _, marker := range []string{"early-decision-marker", "late-decision-marker"} {
			if slices.Contains(decisions, marker) {
				ranked = append(ranked, marker)
			}
		}
		for _, decision := range decisions {
			if decision != "early-decision-marker" && decision != "late-decision-marker" {
				ranked = append(ranked, decision)
			}
		}
		decisions = ranked[:min(len(ranked), maxIntermediateKeyDecisions)]
		return session.SessionSynthesis{Outcome: strings.Repeat("m", 2_000), KeyDecisions: decisions}, nil
	})

	got, err := runSynthesis(context.Background(), runner, s)
	if err != nil {
		t.Fatal(err)
	}
	if mergeCalls < 3 || !sawCombinedMarkers {
		t.Fatalf("merge calls = %d, combined markers = %v; want recursive merge carrying both", mergeCalls, sawCombinedMarkers)
	}
	if len(got.KeyDecisions) != maxFinalKeyDecisions ||
		!slices.Contains(got.KeyDecisions, "early-decision-marker") ||
		!slices.Contains(got.KeyDecisions, "late-decision-marker") {
		t.Fatalf("final key decisions = %#v, want 8 including early and late markers", got.KeyDecisions)
	}
}
