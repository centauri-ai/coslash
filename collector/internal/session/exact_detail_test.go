package session

import (
	"context"
	"errors"
	"testing"
)

type cancelDetailContext struct {
	context.Context
	remaining int
}

func (ctx *cancelDetailContext) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestLocalDetailRevisionStopsDuringChangeHashing(t *testing.T) {
	edits := make([]FileEdit, 100)
	for index := range edits {
		edits[index] = FileEditWithChanges("file.go", 1, 0, 1, false, []FileChange{{Kind: "diff", Text: "change"}})
	}
	ctx := &cancelDetailContext{Context: context.Background(), remaining: 10}

	revision, err := LocalDetailRevisionContext(ctx, Session{SessionDetails: SessionDetails{FileEdits: edits}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if revision != "" {
		t.Fatalf("revision = %q, want empty", revision)
	}
}

func TestPiRuntimeModalityDoesNotChangeExactRevision(t *testing.T) {
	rpc, sdk := "pi-rpc", "pi-sdk"
	for _, agent := range []string{"pi", "codex"} {
		value := Session{Agent: agent, Entrypoint: &rpc}
		original, err := LocalDetailRevision(value)
		if err != nil {
			t.Fatal(err)
		}
		value.Entrypoint = &sdk
		changed, err := LocalDetailRevision(value)
		if err != nil {
			t.Fatal(err)
		}
		if (original == changed) != (agent == "pi") {
			t.Fatalf("%s entrypoint revision invariant violated", agent)
		}
		if *value.Entrypoint != sdk {
			t.Fatal("caller mutated")
		}
	}
}
