//go:build unix

package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunSSHCommandKillsProcessGroupOnOutputFlood(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-pid")
	t.Setenv("COSLASH_FAKE_SPAWN_CHILD", "1")
	t.Setenv("COSLASH_FAKE_CHILD_PID", marker)
	options := fakeOptions([]byte(strings.Repeat("x", 100)), 0, "", false)
	options.Limits.MaxStderrBytes = 16
	if err := runSSHCommand(context.Background(), options, []string{"-O", "check", "host"}); !errors.Is(err, ErrStderrLimit) {
		t.Fatalf("runSSHCommand error = %v, want ErrStderrLimit", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("child pid = %q, error = %v", data, err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control command left child process %d running", pid)
}
