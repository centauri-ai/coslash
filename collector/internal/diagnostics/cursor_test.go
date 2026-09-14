package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/collector"
)

func TestCursorDiagnosticsProbeIDEAndCLISeparately(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"cursor", "agent"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	source := collectSource(context.Background(), t.TempDir(), collector.SourceHealth{Agent: "cursor"}, false)
	if source.CLI.Name != "agent" || !source.CLI.Found {
		t.Fatalf("Cursor CLI probe = %#v", source.CLI)
	}
	if source.IDE == nil || source.IDE.Name != "cursor" || !source.IDE.Found {
		t.Fatalf("Cursor IDE probe = %#v", source.IDE)
	}
}
