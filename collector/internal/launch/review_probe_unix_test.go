//go:build !windows

package launch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRemoteReviewerOptionsRejectUnsupportedClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCLI := func(name, help string) {
		t.Helper()
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+help+"'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeCLI("claude", "--safe-mode --strict-mcp-config --tools")
	writeCLI("codex", "--ephemeral --ignore-user-config --ignore-rules --disable --sandbox")
	original := reviewCommandContext
	t.Cleanup(func() { reviewCommandContext = original })
	reviewCommandContext = func(ctx context.Context, bin string, args ...string) *exec.Cmd {
		if bin != "ssh" {
			t.Fatalf("binary = %q", bin)
		}
		return exec.CommandContext(ctx, "sh", "-c", args[len(args)-1])
	}
	options, err := RemoteReviewerOptions(context.Background(), "agent-box")
	if err != nil || !reflect.DeepEqual(options, []ReviewerOption{{ID: "codex", Label: "Codex CLI", Executable: "codex"}}) {
		t.Fatalf("unsupported Claude options = %#v, %v", options, err)
	}
	writeCLI("claude", "--safe-mode --restricted --strict-mcp-config --tools")
	options, err = RemoteReviewerOptions(context.Background(), "agent-box")
	if err != nil || !reflect.DeepEqual(options, ReviewerOptions()[:2]) {
		t.Fatalf("supported Claude options = %#v, %v", options, err)
	}
}
