//go:build windows

package codex

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

const holdRolloutEnv = "CODEX_TEST_HOLD_ROLLOUT"

func TestRestartManagerMapsHelperProcessToSession(t *testing.T) {
	id := "01a0b1a9-d254-7162-af80-9e5d3a5afc3e"
	path := filepath.Join(t.TempDir(), "rollout-2026-09-17T10-00-00-"+id+".jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHoldRollout$")
	cmd.Env = append(os.Environ(), holdRolloutEnv+"="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("helper process: %v", err)
		}
	})
	if scanner := bufio.NewScanner(stdout); !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatalf("helper did not open rollout: %q", scanner.Text())
	}

	pids, err := processesUsingRollout(path)
	if err != nil {
		t.Fatal(err)
	}
	if pid := uint32(cmd.Process.Pid); !slices.Contains(pids, pid) {
		t.Fatalf("processesUsingRollout(%q) = %v, want helper PID %d", path, pids, pid)
	}

	live, err := loadLiveSessionsFromFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := live[id]; !ok {
		t.Fatalf("loadLiveSessionsFromFiles(%q) = %v, want session %q", path, live, id)
	}
}

func TestHoldRollout(t *testing.T) {
	path := os.Getenv(holdRolloutEnv)
	if path == "" {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}
