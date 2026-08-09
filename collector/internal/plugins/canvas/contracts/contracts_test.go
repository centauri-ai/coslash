package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFrozenJSONFixtures(t *testing.T) {
	t.Parallel()

	tests := map[string]any{
		"error.json":           &ErrorResponse{},
		"terminal-input.json":  &TerminalClientFrame{},
		"terminal-resize.json": &TerminalClientFrame{},
		"workspace.json":       &WorkspaceDocument{},
		"board.json":           &BoardDocument{},
		"run.json":             &RunDocument{},
	}
	for name, target := range tests {
		name, target := name, target
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, target); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
		})
	}
}

func TestRunFixtureKeepsCompositeSessionIdentity(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run RunDocument
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	if len(run.Sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(run.Sessions))
	}
	if run.Sessions[0].ID != run.Sessions[1].ID || run.Sessions[0].Agent == run.Sessions[1].Agent {
		t.Fatalf("fixture must prove duplicate bare IDs remain distinct: %#v", run.Sessions)
	}
}
