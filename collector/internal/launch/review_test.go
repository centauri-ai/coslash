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
			bin:   "claude",
			args:  []string{"-p", "--name", name, "--permission-mode", "plan"},
			stdin: prompt,
		},
		"codex": {
			bin:   "codex",
			args:  []string{"exec", "--sandbox", "read-only", "--skip-git-repo-check", "-"},
			stdin: prompt,
		},
		"opencode": {
			bin:   "opencode",
			args:  []string{"run", "--title", name, "--dir", "/repo"},
			env:   []string{`OPENCODE_PERMISSION={"edit":"deny","bash":"deny"}`},
			stdin: prompt,
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

func TestBoundedBufferCapsDiagnostics(t *testing.T) {
	buffer := boundedBuffer{limit: 4}
	if written, err := buffer.Write([]byte("secret diagnostic")); err != nil || written != 17 {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if got := buffer.String(); got != "secr" {
		t.Fatalf("String() = %q", got)
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
