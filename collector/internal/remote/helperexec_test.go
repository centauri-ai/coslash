package remote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
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

func TestHelperCollectSuccess(t *testing.T) {
	request, baseline, response := completeResponse(t)
	result, err := HelperCollect(context.Background(), "host", "/helper", request, baseline, fakeOptions(response, 0, "", false))
	if err != nil || !result.RequestComplete || result.Records != 3 {
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

func TestHelperCollectHonorsCancellation(t *testing.T) {
	request, baseline, _ := completeResponse(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := HelperCollect(ctx, "host", "/helper", request, baseline, fakeOptions(nil, 0, "", true))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HelperCollect error = %v, want deadline", err)
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

func completeResponse(t *testing.T) (remoteprotocol.Request, remoteprotocol.Generation, []byte) {
	t.Helper()
	request := remoteprotocol.Request{
		RequestID: "req-1", Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1},
		Schema: remoteprotocol.VersionRange{Min: 1, Max: 1}, ParserVersion: vendors.ParserVersion,
		BaselineMode: remoteprotocol.BaselineKnown, BaselineID: "base-1",
		CollectedAtMs: 1, Vendors: []string{"codex"}, Limits: remoteprotocol.Limits{
			MaxRecordBytes: 1024, MaxResponseBytes: 4096, MaxRecords: 10, MaxInventoryFamilies: 10,
		},
	}
	baseline := remoteprotocol.Generation{BaselineID: "base-1"}
	records := []remoteprotocol.Record{
		{Type: remoteprotocol.RecordHandshake, ProtocolVersion: 1, RequestID: "req-1", Sequence: 1, BaselineID: "base-1", SchemaVersion: 1, ParserVersion: vendors.ParserVersion},
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
	_, _ = os.Stdout.WriteString(os.Getenv("COSLASH_FAKE_OUTPUT"))
	_, _ = os.Stderr.WriteString(os.Getenv("COSLASH_FAKE_STDERR"))
	if os.Getenv("COSLASH_FAKE_HANG") == "true" {
		time.Sleep(time.Hour)
	}
	exitCode, _ := strconv.Atoi(os.Getenv("COSLASH_FAKE_EXIT"))
	os.Exit(exitCode)
}
