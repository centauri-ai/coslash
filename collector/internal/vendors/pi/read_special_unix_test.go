//go:build darwin || linux

package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAncestorFIFOIsUnavailableEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "ancestor.jsonl")
	if err := syscall.Mkfifo(evidence, 0o600); err != nil {
		t.Fatal(err)
	}
	// A preloaded FIFO makes a missing guard fail an assertion without hanging CI.
	pipe, err := os.OpenFile(evidence, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	data, err := json.Marshal(header{Type: "session", Version: 3, ID: "ancestor"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipe.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	fork := captureTranscript(t, "fork", evidence, assistantRow("a", "", "useful", 3))
	captureEnvironment(t, filepath.Dir(fork))
	plan, err := PlanSourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := plan.SessionInputsContext(t.Context(), "fork")
	if err != nil || inputs.complete || len(inputs.Files) != 1 {
		t.Fatalf("FIFO established provenance: %+v, %v", inputs, err)
	}
	parsed, _, err := ParseFrozenSessionContext(t.Context(), inputs, freezeInputs(t, inputs))
	if err != nil || !parsed.Session.TokensUnavailable || !parsed.Session.CostUnavailable || !parsed.Session.DetailsIncomplete || parsed.RecordedCost != nil || parsed.ParentID != "" {
		t.Fatalf("unavailable FIFO ancestor = %+v, %v", parsed, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := plan.SessionInputsContext(ctx, "fork"); !errors.Is(err, context.Canceled) {
		t.Fatalf("planning cancellation = %v", err)
	}
}
