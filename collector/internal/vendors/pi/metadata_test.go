package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testRecord() RuntimeRecord {
	leaf := "leaf"
	return RuntimeRecord{Version: 1, RuntimeID: "owner", PID: os.Getpid(), ProcessStartIdentity: "start", StartedAtMs: 1, SessionID: "custom.id", TranscriptPath: filepath.Join(os.TempDir(), "custom.jsonl"), LeafID: &leaf, WorkState: "idle", Sequence: 1, UpdatedAtMs: 1}
}
func TestOwnerPrecedence(t *testing.T) {
	idle := runtimeEvidence{Record: testRecord()}
	busy := idle
	busy.Record.WorkState = "busy"
	waiting := idle
	waiting.Record.DialogOpen = true
	unknown := idle
	unknown.Record.WorkState = "unknown"
	for _, test := range []struct {
		name   string
		owners []runtimeEvidence
		want   string
	}{
		{"waiting overrides busy", []runtimeEvidence{busy, waiting}, "waiting"},
		{"busy overrides idle", []runtimeEvidence{idle, busy}, "busy"},
		{"unknown prevents idle", []runtimeEvidence{idle, unknown}, "unknown"},
		{"idle", []runtimeEvidence{idle}, "idle"},
		{"missing", nil, "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := statusFor(test.owners, func(runtimeEvidence) string { return "live" }); got != test.want {
				t.Fatalf("got %s want %s", got, test.want)
			}
		})
	}
	if got := statusFor([]runtimeEvidence{idle}, func(runtimeEvidence) string { return "dead" }); got != "inactive" {
		t.Fatal(got)
	}
}
func TestIdentityAndRetainedDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	r := testRecord()
	identity, err := ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	r.ProcessStartIdentity = identity
	if got := ownerState(runtimeEvidence{Record: r}); got != "live" {
		t.Fatal(got)
	}
	r.ProcessStartIdentity = "reused PID"
	if got := ownerState(runtimeEvidence{Record: r}); got != "dead" {
		t.Fatal(got)
	}
	r.ProcessStartIdentity = ""
	if got := ownerState(runtimeEvidence{Record: r}); got != "unknown" {
		t.Fatal(got)
	}
	if err := os.MkdirAll(filepath.Join(home, "pi-history"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(runtimeEvidence{Record: r, Exited: true})
	if err := os.WriteFile(filepath.Join(home, "pi-history", "retained.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := RuntimeTranscriptPaths()
	if err != nil || len(paths) != 1 || paths[0] != r.TranscriptPath {
		t.Fatalf("%v %v", paths, err)
	}
	metadata, err := LoadMetadata()
	if err != nil || metadata.Session(r.SessionID).Live != "inactive" {
		t.Fatalf("%v %v", metadata, err)
	}
	data = []byte(`{"version":1,"runtimeId":"owner","pid":1}`)
	if err := os.WriteFile(filepath.Join(home, "pi-history", "retained.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	paths, _ = RuntimeTranscriptPaths()
	if len(paths) != 0 {
		t.Fatal(paths)
	}
}
func TestEnsureExtensionPreservesUnmanagedFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", root)
	settings := filepath.Join(root, "settings.json")
	os.WriteFile(settings, []byte(`{"extensions":["user.ts"]}`), 0600)
	if err := EnsureExtension(); err != nil {
		t.Fatal(err)
	}
	if !ExtensionDiagnostics().Installed {
		t.Fatal("not installed")
	}
	target, _ := ExtensionPath()
	os.WriteFile(target, []byte("// user-owned"), 0600)
	if err := EnsureExtension(); err == nil {
		t.Fatal("overwrote unmanaged extension")
	}
	contents, _ := os.ReadFile(settings)
	if string(contents) != `{"extensions":["user.ts"]}` {
		t.Fatal("modified settings")
	}
}

func TestRuntimeSequenceAndLeafAgreement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	for _, kind := range []string{"pi-runtime", "pi-history"} {
		if err := os.MkdirAll(filepath.Join(home, kind), 0700); err != nil {
			t.Fatal(err)
		}
	}
	r := testRecord()
	identity, err := ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	r.ProcessStartIdentity = identity
	old := r
	old.Sequence = 2
	old.WorkState = "busy"
	oldData, _ := json.Marshal(runtimeEvidence{Record: old})
	os.WriteFile(filepath.Join(home, "pi-history", "old.json"), oldData, 0600)
	r.Sequence = 3
	data, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(home, "pi-runtime", r.RuntimeID+".json"), data, 0600)
	records, err := runtimeRecords()
	if err != nil || len(records) != 1 || records[0].Record.Sequence != 3 {
		t.Fatalf("%v %v", records, err)
	}
	if leaf, ok := RuntimeLeaf(r.SessionID, r.TranscriptPath); !ok || leaf != "leaf" {
		t.Fatalf("%s %v", leaf, ok)
	}
	snapshot, err := LoadRuntimeSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRuntimeSnapshot(context.Background(), snapshot)
	other := r
	other.RuntimeID = "other"
	otherLeaf := "alternative"
	other.LeafID = &otherLeaf
	data, _ = json.Marshal(other)
	os.WriteFile(filepath.Join(home, "pi-runtime", "other.json"), data, 0600)
	if _, ok := RuntimeLeaf(r.SessionID, r.TranscriptPath); ok {
		t.Fatal("ambiguous leaf accepted")
	}
	// This refresh retains one generation even if metadata changes during projection.
	if leaf, ok := runtimeLeafContext(ctx, r.SessionID, r.TranscriptPath); !ok || leaf != "leaf" {
		t.Fatal("snapshot reread runtime metadata")
	}
	if err := os.RemoveAll(filepath.Join(home, "pi-runtime")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "pi-history")); err != nil {
		t.Fatal(err)
	}
	metadata, err := LoadMetadataContext(ctx)
	if err != nil || metadata.Session(r.SessionID).Live != "idle" {
		t.Fatalf("lost verified owner snapshot: %v, %v", metadata, err)
	}
	paths, err := RuntimeTranscriptPathsContext(ctx)
	if err != nil || len(paths) != 1 || paths[0] != r.TranscriptPath {
		t.Fatalf("lost snapshot discovery: %v, %v", paths, err)
	}
}

func TestQuotedTildeAgentDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", "~/custom-agent")
	t.Setenv("COSLASH_HOME", filepath.Join(home, "coslash"))
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	agent := filepath.Join(home, "custom-agent")
	if err := os.MkdirAll(agent, 0700); err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(home, "custom-history")
	data, _ := json.Marshal(map[string]string{"sessionDir": storage})
	if err := os.WriteFile(filepath.Join(agent, "settings.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	target, err := ExtensionPath()
	if err != nil || target != filepath.Join(agent, "extensions", "coslash-extension.ts") {
		t.Fatalf("quoted tilde extension path: %q %v", target, err)
	}
	if err := EnsureExtension(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
	roots := ConfiguredSessionRoots()
	if len(roots) != 1 || roots[0] != storage {
		t.Fatalf("quoted tilde settings roots: %v", roots)
	}
}

func TestRuntimeLeafCanonicalPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	real := filepath.Join(home, "real")
	if err := os.MkdirAll(real, 0700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(real, "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(transcript)
	if err != nil {
		t.Fatal(err)
	}
	r := testRecord()
	r.TranscriptPath = filepath.Join(alias, "session.jsonl")
	r.ProcessStartIdentity, err = ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(home, "pi-runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(r)
	recordPath := filepath.Join(runtimeDir, r.RuntimeID+".json")
	if err := os.WriteFile(recordPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if leaf, ok := RuntimeLeaf(r.SessionID, canonical); !ok || leaf != "leaf" {
		t.Fatalf("canonical leaf: %q %v", leaf, ok)
	}
	r.ProcessStartIdentity = "reused PID"
	data, _ = json.Marshal(r)
	if err := os.WriteFile(recordPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := RuntimeLeaf(r.SessionID, canonical); ok {
		t.Fatal("path normalization accepted a dead owner")
	}
}

func TestRuntimeMetadataCancellation(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, injected := range []bool{false, true} {
		current := ctx
		if injected {
			current = WithRuntimeSnapshot(current, &RuntimeSnapshot{})
		}
		if _, err := LoadMetadataContext(current); !errors.Is(err, context.Canceled) {
			t.Errorf("metadata injected=%v: %v", injected, err)
		}
		if _, err := RuntimeTranscriptPathsContext(current); !errors.Is(err, context.Canceled) {
			t.Errorf("paths injected=%v: %v", injected, err)
		}
	}
}
