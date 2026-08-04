package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
)

func TestRenderDoctorAndExitCode(t *testing.T) {
	snapshot := &diagnostics.Snapshot{
		Version: "test",
		Storage: diagnostics.Storage{Home: "~/.coslash", Writable: true},
		Sources: []diagnostics.Source{{
			Label: "Claude Code", Root: "~/.claude/projects", Transcripts: 2, Sessions: 1,
			CLI: diagnostics.CLI{Found: true, Path: "/usr/bin/claude", Version: "1.0"},
		}},
		Checks: []diagnostics.Check{{Title: "Claude Code sessions", Status: diagnostics.StatusFail, Detail: "broken"}},
	}
	var output bytes.Buffer
	renderDoctor(&output, snapshot)
	got := output.String()
	for _, wanted := range []string{"coSlash test", "FAIL", "Claude Code sessions", "~/.claude/projects"} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("output missing %q: %s", wanted, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("output contains ANSI: %q", got)
	}
	if doctorExitCode(snapshot) != 1 {
		t.Fatal("fail check should return exit code 1")
	}
	snapshot.Checks[0].Status = diagnostics.StatusWarn
	if doctorExitCode(snapshot) != 0 {
		t.Fatal("warn-only snapshot should return exit code 0")
	}
}
