package diagnostics

import (
	"strings"
	"testing"
)

func TestDeriveSourceAndPlatformChecks(t *testing.T) {
	snapshot := &Snapshot{
		Platform:  Platform{OS: "linux"},
		Storage:   Storage{Home: "~/.coslash", Writable: false, Error: "permission denied"},
		Synthesis: Synthesis{Enabled: false},
		Sources: []Source{
			{Agent: "claude", Label: "Claude Code", Root: "~/.claude/projects", State: SourceMissing, Skipped: []SkippedPath{}, CLI: CLI{Name: "claude"}},
			{Agent: "codex", Label: "Codex", Root: "~/.codex/sessions", State: SourceEmpty, Skipped: []SkippedPath{}, CLI: CLI{Name: "codex"}},
		},
	}
	checks := derive(snapshot)
	want := []struct {
		id     string
		status Status
	}{
		{"source.claude", StatusWarn},
		{"source.codex", StatusWarn},
		{"sources.none", StatusFail},
		{"storage", StatusFail},
		{"synthesis", StatusOK},
		{"platform.terminal", StatusWarn},
	}
	if len(checks) != len(want) {
		t.Fatalf("checks = %#v", checks)
	}
	for index, expected := range want {
		if checks[index].ID != expected.id || checks[index].Status != expected.status {
			t.Fatalf("check %d = %#v, want %s/%s", index, checks[index], expected.id, expected.status)
		}
	}
}

func TestDeriveFailsWhenNoSourceProbesAreAvailable(t *testing.T) {
	snapshot := &Snapshot{
		Platform:  Platform{OS: "darwin", TerminalLaunchSupported: true},
		Storage:   Storage{Home: "~/.coslash", Writable: true},
		Synthesis: Synthesis{Enabled: false},
		Sources:   []Source{},
	}
	checks := derive(snapshot)
	if checks[0].ID != "sources.none" || checks[0].Status != StatusFail {
		t.Fatalf("first check = %#v", checks[0])
	}
}

func TestDeriveUnreadableAndUnparsedSources(t *testing.T) {
	tests := []struct {
		name   string
		source Source
		want   Status
	}{
		{name: "unreadable", source: Source{Agent: "claude", Label: "Claude Code", Root: "~/.claude/projects", State: SourceUnreadable, Error: "denied", CLI: CLI{Name: "claude"}}, want: StatusFail},
		{name: "unparsed", source: Source{Agent: "claude", Label: "Claude Code", Root: "~/.claude/projects", State: SourceOK, Transcripts: 2, CLI: CLI{Name: "claude", Found: true}}, want: StatusFail},
		{name: "skipped", source: Source{Agent: "claude", Label: "Claude Code", Root: "~/.claude/projects", State: SourceOK, Transcripts: 2, Sessions: 1, SkippedTotal: 1, CLI: CLI{Name: "claude", Found: true}}, want: StatusWarn},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := &Snapshot{
				Platform:  Platform{OS: "darwin", TerminalLaunchSupported: true},
				Storage:   Storage{Home: "~/.coslash", Writable: true},
				Synthesis: Synthesis{Enabled: false},
				Sources:   []Source{test.source},
			}
			checks := derive(snapshot)
			if checks[0].Status != test.want {
				t.Fatalf("source status = %s, want %s", checks[0].Status, test.want)
			}
			for _, check := range checks {
				if check.ID == "cli.claude" && test.source.Transcripts == 0 {
					t.Fatal("CLI warning should be suppressed without transcripts")
				}
			}
		})
	}
}

func TestDeriveReportsTheFirstPartialScanFailure(t *testing.T) {
	source := Source{
		Agent:        "claude",
		Label:        "Claude Code",
		Root:         "~/.claude/projects",
		State:        SourceOK,
		Transcripts:  2,
		Sessions:     1,
		SkippedTotal: 1,
		Skipped:      []SkippedPath{{Path: "~/.claude/projects/blocked", Error: "permission denied"}},
		CLI:          CLI{Name: "claude", Found: true},
	}
	check := sourceCheck(source, "")
	if check.Status != StatusWarn || !strings.Contains(check.Detail, "blocked") || !strings.Contains(check.Detail, "permission denied") {
		t.Fatalf("check = %#v", check)
	}
}
