package remote

import (
	"strings"
	"testing"
	"time"
)

func TestSnapshotCommandExact(t *testing.T) {
	got, err := SnapshotCommand(1786557600000, 1787162399000)
	if err != nil {
		t.Fatal(err)
	}
	want := `exec "$HOME/.local/bin/coslash" snapshot --since 1786557600000 --request-now 1787162399000 --agents claude,codex`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestProbeCommandExact(t *testing.T) {
	want := `exec "$HOME/.local/bin/coslash" snapshot --probe`
	if ProbeCommand() != want {
		t.Fatalf("got %q", ProbeCommand())
	}
}

func TestSSHArgvRejectsHostileAliases(t *testing.T) {
	for _, alias := range []string{"-o", "--BatchMode=no", "user@host", "a b", ""} {
		if _, err := SSHArgv(alias, ProbeCommand()); err == nil {
			t.Fatalf("expected reject for %q", alias)
		}
	}
	args, err := SSHArgv("gpu-server", ProbeCommand())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "--", "gpu-server", ProbeCommand()}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %#v", args)
	}
}

func TestSnapshotCommandRejectsInvalidEpochs(t *testing.T) {
	if _, err := SnapshotCommand(2, 1); err == nil {
		t.Fatal("expected since > requestNow error")
	}
	if _, err := SnapshotCommand(-1, 1); err == nil {
		t.Fatal("expected negative error")
	}
}

func TestRetryBackoffCapsAtThirtyMinutes(t *testing.T) {
	if got := retryBackoff(1); got != 3*time.Minute {
		t.Fatalf("failures=1: %v", got)
	}
	if got := retryBackoff(2); got != 6*time.Minute {
		t.Fatalf("failures=2: %v", got)
	}
	if got := retryBackoff(3); got != 12*time.Minute {
		t.Fatalf("failures=3: %v", got)
	}
	if got := retryBackoff(4); got != 24*time.Minute {
		t.Fatalf("failures=4: %v", got)
	}
	if got := retryBackoff(5); got != 30*time.Minute {
		t.Fatalf("failures=5: %v", got)
	}
	if got := retryBackoff(20); got != 30*time.Minute {
		t.Fatalf("failures=20: %v", got)
	}
}
