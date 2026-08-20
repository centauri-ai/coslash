package remote

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func TestClassifyExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		result RunResult
		trans  error
		probe  bool
		state  State
		reason Reason
	}{
		{"127", RunResult{ExitCode: 127}, nil, true, StateSetupRequired, ReasonCollectorMissing},
		{"255", RunResult{ExitCode: 255}, remoteviewv1.ErrMissingFrame, false, StateError, ReasonConnectionFailed},
		{"probe missing frame", RunResult{ExitCode: 0}, remoteviewv1.ErrMissingFrame, true, StateUpgradeRequired, ReasonCollectorOutdated},
		{"snapshot bad frame", RunResult{ExitCode: 0}, remoteviewv1.ErrInvalidFrame, false, StateError, ReasonInvalidRemoteTransport},
		{"timeout", RunResult{ExitCode: -1}, nil, false, StateError, ReasonRefreshTimeout},
	}
	for _, tc := range cases {
		got := classifyRunFailure(tc.result, nil, tc.trans, tc.probe)
		if got.State != tc.state || got.Reason != tc.reason {
			t.Fatalf("%s: %+v", tc.name, got)
		}
	}
}

func TestRunnerRejectsHostileAliasBeforeExec(t *testing.T) {
	runner := NewRunner()
	runner.exec = func(ctx context.Context, bin string, args []string, stdin []byte, limits RunLimits) (RunResult, error) {
		t.Fatal("exec should not run")
		return RunResult{}, nil
	}
	_, err := runner.Run(context.Background(), "--oops", ProbeCommand(), nil, RunLimits{})
	if err == nil {
		t.Fatal("expected alias error")
	}
}

func TestCappedBufferOverflow(t *testing.T) {
	var buf cappedBuffer
	buf.limit = 4
	_, _ = buf.Write([]byte("abcdef"))
	if !buf.overflow || string(buf.bytes()) != "abcd" {
		t.Fatalf("overflow=%v bytes=%q", buf.overflow, buf.bytes())
	}
}

func TestRedactDiagnostic(t *testing.T) {
	got := redactDiagnostic("fail /Users/helu/.ssh/id_rsa user@host denied")
	if strings.Contains(got, "/Users") || strings.Contains(got, "user@host") {
		t.Fatalf("not redacted: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected redaction markers: %q", got)
	}
}

func TestExtractAndDecodeRejectsDuplicateFrames(t *testing.T) {
	payload, err := remoteviewv1.Marshal(sampleView(0, 20_000))
	if err != nil {
		t.Fatal(err)
	}
	frame := mustFramePayload(t, payload)
	stdout := append(append([]byte{}, frame...), frame...)
	_, _, err = extractAndDecodeView(stdout)
	if !errors.Is(err, remoteviewv1.ErrDuplicateFrame) {
		t.Fatalf("err=%v", err)
	}
}

func TestManagerStdoutOverflowRetainsCache(t *testing.T) {
	clock := &testClock{t: time.UnixMilli(11_000_000)}
	var n int
	fake := &FakeRunner{Hook: func(call FakeCall) (RunResult, error) {
		n++
		now := clock.Now()
		if call.RemoteCommand == ProbeCommand() {
			return RunResult{Stdout: framedProbe(t), StartedAt: now, FinishedAt: now}, nil
		}
		if n == 2 {
			return RunResult{Stdout: framedView(t, 0, now.UnixMilli()), StartedAt: now, FinishedAt: now}, nil
		}
		return RunResult{Overflow: ErrStdoutOverflow, StartedAt: now, FinishedAt: now}, ErrStdoutOverflow
	}}
	mgr := NewManager(Options{Runner: fake, Cache: NewCache(t.TempDir()), Now: clock.Now})
	_ = mgr.ApplySettings(&settings.RemoteSettings{ID: "r_0123456789abcdef", SSHAlias: "gpu", Enabled: true})
	waitUntil(t, 2*time.Second, func() bool { return mgr.DiagnosticsHealth().State == StateOK })
	clock.Advance(FreshnessInterval + time.Second)
	_ = mgr.ListView(0)
	waitUntil(t, 2*time.Second, func() bool {
		h := mgr.DiagnosticsHealth()
		return h.State == StateStale && !h.Refreshing
	})
	if len(mgr.ListView(0).Sessions) != 1 {
		t.Fatal("expected retained sessions")
	}
}
