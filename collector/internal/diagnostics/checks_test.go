package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorDiagnosticsTreatEitherLaneAsInstalled(t *testing.T) {
	for _, source := range []Source{
		{Agent: "cursor", Label: "Cursor", State: SourceOK, Entries: 1, Sessions: 1, CLI: CLI{Name: "agent"}, IDE: &CLI{Name: "cursor", Found: true}},
		{Agent: "cursor", Label: "Cursor", State: SourceOK, Entries: 1, Sessions: 1, CLI: CLI{Name: "agent", Found: true}, IDE: &CLI{Name: "cursor"}},
	} {
		for _, check := range derive(&Snapshot{Sources: []Source{source}}) {
			if strings.HasPrefix(check.ID, "cli.cursor") {
				t.Fatalf("unexpected Cursor CLI warning with an installed lane: %#v", check)
			}
		}
	}
}

func TestCursorEmptySourceNamesBothLanes(t *testing.T) {
	check := sourceCheck(Source{Agent: "cursor", Label: "Cursor", Root: "/cursor", State: SourceEmpty, CLI: CLI{Name: "agent"}, IDE: &CLI{Name: "cursor"}})
	if !strings.Contains(check.Fix, "Cursor IDE") || !strings.Contains(check.Fix, "agent") {
		t.Fatalf("fix = %q, want both Cursor lanes", check.Fix)
	}
}

func TestCursorIDEExecutableFindsApplicationBundle(t *testing.T) {
	t.Setenv("PATH", "")
	home := t.TempDir()
	path := filepath.Join(home, "Applications", "Cursor.app", "Contents", "MacOS", "Cursor")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := cursorIDEExecutable(home); got != path {
		t.Fatalf("Cursor executable = %q, want %q", got, path)
	}
}
