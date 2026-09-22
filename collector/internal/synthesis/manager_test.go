package synthesis

import (
	"context"
	"errors"
	"fmt"
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
	digest := make([]session.DigestEntry, 30)
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
