package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestHelperCollectRejectsTrailingOutput(t *testing.T) {
	request, baseline, response := completeResponse(t)
	response = append(response, []byte("{}\n")...)
	_, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, fakeOptions(response, 0, "", false))
	if !errors.Is(err, ErrHelperFailed) {
		t.Fatalf("HelperCollect error = %v, want ErrHelperFailed", err)
	}
}

func TestHelperProcessErrorPreservesUnrelatedExitCodeAfterCleanup(t *testing.T) {
	process := &helperProcess{terminationRequested: true, stderr: &cappedStderr{}}
	if err := helperProcessError(context.Background(), 1, errors.New("leader failed"), process); err == nil {
		t.Fatal("helperProcessError() hid the leader's exit status")
	}
}

func TestHelperCollectSuccess(t *testing.T) {
	request, baseline, response := completeResponse(t)
	result, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, fakeOptions(response, 0, "", false))
	if err != nil || !result.RequestComplete || result.Records != 3 {
		t.Fatalf("HelperCollect result = %#v, error = %v", result, err)
	}
}

func TestHelperCollectMarksSkippedCompletionAsTruncated(t *testing.T) {
	request, baseline, _ := completeResponse(t)
	records := []remoteprotocol.Record{
		{Type: remoteprotocol.RecordHandshake, ProtocolVersion: 1, RequestID: "req-1", Sequence: 1, BaselineID: "base-1", SchemaVersion: remotefacts.SchemaVersion, ParserVersion: vendors.ParserVersion},
		{Type: remoteprotocol.RecordVendorComplete, ProtocolVersion: 1, RequestID: "req-1", Sequence: 2, Vendor: "codex", EnumerationComplete: true, InventoryComplete: true, Counts: remoteprotocol.Counts{CandidateFamilies: 2, SelectedFamilies: 1, SkippedFamilies: 1}},
		{Type: remoteprotocol.RecordRequestComplete, ProtocolVersion: 1, RequestID: "req-1", Sequence: 3},
	}
	response, encodeErr := remoteprotocol.Encode(records)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	result, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, fakeOptions(response, 0, "", false))
	if err != nil || !result.RequestComplete || len(result.Coverage) != 1 || !result.Coverage[0].Truncated {
		t.Fatalf("HelperCollect result = %#v, error = %v", result, err)
	}
}

func TestHelperCollectRejectsTruncatedResponse(t *testing.T) {
	request, baseline, response := completeResponse(t)
	firstLine := response[:strings.IndexByte(string(response), '\n')+1]
	response = append(firstLine, []byte(`{"type":"changed_family"`)...)
	result, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, fakeOptions(response, 0, "", false))
	if !errors.Is(err, ErrHelperFailed) {
		t.Fatalf("HelperCollect error = %v, want ErrHelperFailed", err)
	}
	if result.Records != 1 || len(result.Proposal.Families) != 0 {
		t.Fatalf("partial record mutated proposal: %#v", result)
	}
}

func TestHelperCollectRejectsInterruptedOrMalformedStreamAfterWholeChangedRecord(t *testing.T) {
	request, baseline, _ := completeResponse(t)
	request.Limits.MaxRecordBytes = 16 << 10
	request.Limits.MaxResponseBytes = 32 << 10
	family := validFamily(t, "root")
	family.Vendor = vendors.AgentCodex
	records := []remoteprotocol.Record{
		{Type: remoteprotocol.RecordHandshake, ProtocolVersion: 1, RequestID: request.RequestID, Sequence: 1, BaselineID: request.BaselineID, SchemaVersion: remotefacts.SchemaVersion, ParserVersion: vendors.ParserVersion},
		{Type: remoteprotocol.RecordChanged, ProtocolVersion: 1, RequestID: request.RequestID, Sequence: 2, Vendor: vendors.AgentCodex, FamilyID: "root", Fingerprint: "new", Family: &family},
	}
	response, err := remoteprotocol.Encode(records)
	if err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string][]byte{
		"interrupted": response,
		"malformed":   append(append([]byte(nil), response...), []byte("{not-json\n")...),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, fakeOptions(output, 0, "", false))
			if !errors.Is(err, ErrHelperFailed) || result.RequestComplete {
				t.Fatalf("HelperCollect result = %#v, error = %v", result, err)
			}
			if got := result.Proposal.Families[remoteprotocol.FamilyKey{Vendor: vendors.AgentCodex, FamilyID: "root"}].Fingerprint; got != "new" {
				t.Fatalf("diagnostic proposal fingerprint = %q, want new", got)
			}
		})
	}
}

func TestHelperRefreshDoesNotDowngradeInterruptedChangedResponse(t *testing.T) {
	t.Setenv("COSLASH_FAKE_PARTIAL_CHANGED", "1")
	baseline := CachedSnapshotV2{SourceID: "r_0123456789abcdef"}
	outcome, err := helperRefreshWithOpen(
		context.Background(), "host", 0, time.Unix(3_000, 0), baseline,
		helperTarget{path: "/helper"}, fakeOptions(nil, 0, "", false),
	)
	if !errors.Is(err, ErrHelperFailed) {
		t.Fatalf("helperRefreshWithOpen error = %v, want ErrHelperFailed", err)
	}
	if outcome.Snapshot.RequestComplete || len(outcome.Snapshot.FullRecords) != 1 {
		t.Fatalf("diagnostic partial proposal = %#v", outcome.Snapshot)
	}
}

func TestHelperCapabilitiesRejectsIncompatibleRange(t *testing.T) {
	output := []byte(`{"protocol":{"min":2,"max":2},"schema":{"min":1,"max":1},"parser_version":"parsers-1"}` + "\n")
	_, _, err := HelperCapabilities(context.Background(), "host", "/helper", fakeOptions(output, 0, "", false))
	if !errors.Is(err, ErrHelperIncompatible) {
		t.Fatalf("HelperCapabilities error = %v, want ErrHelperIncompatible", err)
	}
}

func TestHelperCollectBoundsFloodsAndClassifiesExit(t *testing.T) {
	request, baseline, _ := completeResponse(t)
	tests := []struct {
		name   string
		output []byte
		stderr string
		exit   int
		want   error
	}{
		{name: "stdout", output: []byte(strings.Repeat("x", request.Limits.MaxRecordBytes+2)), want: ErrHelperOutputLimit},
		{name: "stderr", stderr: strings.Repeat("x", 100), want: ErrStderrLimit},
		{name: "resource exit", exit: helperExitResource, want: ErrHelperOutputLimit},
		{name: "missing", exit: helperExitShellNotFound, want: ErrHelperMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := fakeOptions(test.output, test.exit, test.stderr, false)
			options.Limits.MaxStderrBytes = 16
			_, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, options)
			if !errors.Is(err, test.want) {
				t.Fatalf("HelperCollect error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestHelperCollectCleansUpChildThatHangsAfterCompletion(t *testing.T) {
	request, baseline, response := completeResponse(t)
	oldGrace := helperExitGrace
	helperExitGrace = 100 * time.Millisecond
	defer func() { helperExitGrace = oldGrace }()
	started := time.Now()
	result, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, fakeOptions(response, 0, "", true))
	if err != nil || !result.RequestComplete {
		t.Fatalf("HelperCollect result = %#v, error = %v", result, err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("hung helper cleanup exceeded bound")
	}
}

func TestHelperCollectPreservesLeaderFailureWhenCleanupKillsDescendant(t *testing.T) {
	request, baseline, response := completeResponse(t)
	oldGrace := helperExitGrace
	helperExitGrace = 100 * time.Millisecond
	defer func() { helperExitGrace = oldGrace }()
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	options := OpenOptions{
		Limits: Limits{Deadline: 2 * time.Second, MaxStderrBytes: 1024},
		command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperExecProcess", "--")
			cmd.Env = append(os.Environ(),
				"COSLASH_FAKE_HELPER=1",
				"COSLASH_FAKE_SPAWN_CHILD=1",
				"COSLASH_FAKE_CHILD_PID="+pidPath,
				"COSLASH_FAKE_OUTPUT="+string(response),
				"COSLASH_FAKE_STDERR=genuine leader failure",
				"COSLASH_FAKE_EXIT="+strconv.Itoa(helperExitInternal),
			)
			return cmd
		},
	}
	_, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, options)
	if !errors.Is(err, ErrHelperFailed) {
		t.Fatalf("HelperCollect error = %v, want ErrHelperFailed", err)
	}
}

func TestHelperCollectHonorsCancellation(t *testing.T) {
	request, baseline, _ := completeResponse(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := HelperCollect(ctx, "host", "/helper", request, baseline, fakeOptions(nil, 0, "", true))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HelperCollect error = %v, want deadline", err)
	}
}

func TestWaitProcessContextTerminatesBeforeWaitingOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	waitStarted := make(chan struct{})
	terminated := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- waitProcessContext(ctx, func() error {
			close(waitStarted)
			<-terminated
			return errors.New("terminated")
		}, func() { close(terminated) })
	}()
	<-waitStarted
	cancel()
	if err := <-done; err == nil || err.Error() != "terminated" {
		t.Fatalf("waitProcessContext() error = %v, want terminated", err)
	}
}

func TestHelperArgsRejectInjection(t *testing.T) {
	for _, input := range []struct{ alias, path string }{
		{alias: "-oProxyCommand=bad", path: "/helper"},
		{alias: "host", path: "/helper;bad"},
		{alias: "host", path: "~/../helper"},
	} {
		if _, err := HelperArgs(input.alias, input.path, HelperCommandCollect, 1); err == nil {
			t.Fatalf("accepted alias %q path %q", input.alias, input.path)
		}
	}
}

func TestRunSSHCommandBoundsControlOutput(t *testing.T) {
	options := fakeOptions([]byte(strings.Repeat("x", 100)), 0, "", false)
	options.Limits.MaxStderrBytes = 16
	err := runSSHCommand(context.Background(), options, []string{"-O", "check", "host"})
	if !errors.Is(err, ErrStderrLimit) {
		t.Fatalf("runSSHCommand error = %v, want ErrStderrLimit", err)
	}
}

func completeResponse(t *testing.T) (remoteprotocol.Request, remoteprotocol.Generation, []byte) {
	t.Helper()
	request := remoteprotocol.Request{
		RequestID: "req-1", Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1},
		Schema: remoteprotocol.VersionRange{Min: remotefacts.SchemaVersion, Max: remotefacts.SchemaVersion}, ParserVersion: vendors.ParserVersion,
		BaselineMode: remoteprotocol.BaselineKnown, BaselineID: "base-1",
		CollectedAtMs: 1, Vendors: []string{"codex"}, Limits: remoteprotocol.Limits{
			MaxRecordBytes: 1024, MaxResponseBytes: 4096, MaxRecords: 10, MaxInventoryFamilies: 10,
		},
	}
	baseline := remoteprotocol.Generation{BaselineID: "base-1"}
	records := []remoteprotocol.Record{
		{Type: remoteprotocol.RecordHandshake, ProtocolVersion: 1, RequestID: "req-1", Sequence: 1, BaselineID: "base-1", SchemaVersion: remotefacts.SchemaVersion, ParserVersion: vendors.ParserVersion},
		{Type: remoteprotocol.RecordVendorComplete, ProtocolVersion: 1, RequestID: "req-1", Sequence: 2, Vendor: "codex", EnumerationComplete: true, InventoryComplete: true, Inventory: []string{}},
		{Type: remoteprotocol.RecordRequestComplete, ProtocolVersion: 1, RequestID: "req-1", Sequence: 3},
	}
	response, err := remoteprotocol.Encode(records)
	if err != nil {
		t.Fatal(err)
	}
	return request, baseline, response
}

func fakeOptions(output []byte, exitCode int, stderr string, hang bool) OpenOptions {
	return OpenOptions{
		Limits: Limits{Deadline: 2 * time.Second, MaxStderrBytes: 1024},
		command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperExecProcess", "--")
			cmd.Env = append(os.Environ(),
				"COSLASH_FAKE_HELPER=1",
				"COSLASH_FAKE_OUTPUT="+string(output),
				"COSLASH_FAKE_STDERR="+stderr,
				"COSLASH_FAKE_EXIT="+strconv.Itoa(exitCode),
				"COSLASH_FAKE_HANG="+strconv.FormatBool(hang),
			)
			return cmd
		},
	}
}

func TestHelperExecProcess(t *testing.T) {
	if os.Getenv("COSLASH_FAKE_HELPER") != "1" {
		return
	}
	if os.Getenv("COSLASH_FAKE_CHILD") == "1" {
		time.Sleep(time.Hour)
		return
	}
	if os.Getenv("COSLASH_FAKE_PARTIAL_CHANGED") == "1" {
		writePartialChangedResponse(t)
		os.Exit(0)
	}
	if os.Getenv("COSLASH_FAKE_SPAWN_CHILD") == "1" {
		child := exec.Command(os.Args[0], "-test.run=TestHelperExecProcess", "--")
		child.Env = append(os.Environ(), "COSLASH_FAKE_CHILD=1")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(97)
		}
		if err := os.WriteFile(os.Getenv("COSLASH_FAKE_CHILD_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(98)
		}
	}
	if path := os.Getenv("COSLASH_FAKE_STDIN_FILE"); path != "" {
		input, err := io.ReadAll(os.Stdin)
		if err != nil || os.WriteFile(path, input, 0o600) != nil {
			os.Exit(99)
		}
	}
	_, _ = os.Stdout.WriteString(os.Getenv("COSLASH_FAKE_OUTPUT"))
	_, _ = os.Stderr.WriteString(os.Getenv("COSLASH_FAKE_STDERR"))
	if os.Getenv("COSLASH_FAKE_HANG") == "true" {
		time.Sleep(time.Hour)
	}
	exitCode, _ := strconv.Atoi(os.Getenv("COSLASH_FAKE_EXIT"))
	os.Exit(exitCode)
}

func writePartialChangedResponse(t *testing.T) {
	var request remoteprotocol.Request
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		t.Fatal(err)
	}
	family := validFamily(t, "root")
	family.Vendor = vendors.AgentCodex
	value := session.Session{
		Agent: vendors.AgentCodex, ID: "root", WorkingDirectory: "/workspace",
		StartedAt: 1, LastActivityTime: 2, Tokens: map[string]session.ModelTokens{},
		Subagents: []session.Subagent{}, SessionDetails: session.SessionDetails{
			Commands: []string{}, Commits: []string{}, CommitSHAs: []string{}, Todos: []session.Todo{},
			Digest: []session.DigestEntry{}, FileEdits: []session.FileEdit{},
		},
	}
	full, err := fullsessionrecord.FromSession(request.SourceID, value)
	if err != nil {
		t.Fatal(err)
	}
	records := []remoteprotocol.Record{
		{Type: remoteprotocol.RecordHandshake, ProtocolVersion: remoteprotocol.ProtocolVersion, RequestID: request.RequestID, Sequence: 1, BaselineID: request.BaselineID, SchemaVersion: remotefacts.SchemaVersion, ParserVersion: vendors.ParserVersion, Capabilities: []string{remoteprotocol.CapabilityFullSessionRecord}},
		{Type: remoteprotocol.RecordChanged, ProtocolVersion: remoteprotocol.ProtocolVersion, RequestID: request.RequestID, Sequence: 2, Vendor: vendors.AgentCodex, FamilyID: "root", Fingerprint: "new", Family: &family, FullRecords: []remoteprotocol.FullRecord{{FamilyID: "root", Record: full}}},
	}
	response, err := remoteprotocol.Encode(records)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.Write(response); err != nil {
		t.Fatal(err)
	}
}
