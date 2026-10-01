//go:build !windows

package synthesis

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestExecuteCommandAddsEnvWithoutSessionMarkers(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CODEX_HOME", "/codex-home")
	output, err := executeCommand(context.Background(), commandSpec{bin: "env", env: []string{"XDG_DATA_HOME=/scratch"}})
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Split(strings.TrimSpace(string(output)), "\n")
	if slices.Contains(env, "CLAUDE_CODE_CHILD_SESSION=1") {
		t.Fatal("synthesis agent inherited a session marker")
	}
	if !slices.Contains(env, "CODEX_HOME=/codex-home") || !slices.Contains(env, "XDG_DATA_HOME=/scratch") {
		t.Fatalf("synthesis environment = %q", env)
	}
}

func TestPiSynthesisReportsCLIAuthFailure(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	directory := t.TempDir()
	path := filepath.Join(directory, "pi")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'SSO expired private-provider-token' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &CLIRunner{Backend: "pi-cli", Bin: path, Model: "default", Timeout: time.Second}
	_, err := runner.Run(context.Background(), "private facts")
	if err == nil || !strings.Contains(err.Error(), "verify CLI authentication and the selected model") || strings.Contains(err.Error(), "private-provider-token") {
		t.Fatalf("failure=%v", err)
	}
	entries, err := os.ReadDir(SynthesisCwd())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("scratch survived failure: %v", entries)
	}
}
