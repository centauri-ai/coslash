package launch

import (
	"reflect"
	"testing"
)

func TestReviewCLICommands(t *testing.T) {
	name := "Review — Bob's change (12345678)"
	prompt := "Review Bob's change\nDo not edit."
	tests := map[string]reviewCommandSpec{
		"claude": {
			bin:  "claude",
			args: []string{"-p", "--name", name, "--permission-mode", "plan", prompt},
		},
		"codex": {
			bin:  "codex",
			args: []string{"exec", "--sandbox", "read-only", "--skip-git-repo-check", prompt},
		},
		"opencode": {
			bin:  "opencode",
			args: []string{"run", "--title", name, "--dir", "/repo", prompt},
			env:  []string{`OPENCODE_PERMISSION={"edit":"deny","bash":"deny"}`},
		},
	}
	for reviewer, want := range tests {
		got, err := reviewCLICommand(reviewer, "/repo", name, prompt)
		if err != nil {
			t.Fatalf("reviewCLICommand(%q): %v", reviewer, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("reviewCLICommand(%q) = %#v, want %#v", reviewer, got, want)
		}
	}
}

func TestReviewerOptionsAreCollectedAgents(t *testing.T) {
	got := ReviewerOptions()
	want := []ReviewerOption{
		{ID: "claude", Label: "Claude Code", Executable: "claude"},
		{ID: "codex", Label: "Codex", Executable: "codex"},
		{ID: "opencode", Label: "OpenCode", Executable: "opencode"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReviewerOptions() = %#v, want %#v", got, want)
	}
}

func TestReviewCLICommandRejectsUnknownReviewer(t *testing.T) {
	if _, err := reviewCLICommand("cursor", "/repo", "name", "prompt"); err == nil {
		t.Fatal("reviewCLICommand() accepted an unsupported reviewer")
	}
}
