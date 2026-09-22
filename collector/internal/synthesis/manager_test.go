package synthesis

import (
	"context"
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
