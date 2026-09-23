package remotehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestSourceEntryLimitIsEnforcedWhileReadingDirectory(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source, err := OpenSource(home, Limits{MaxEntries: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.ReadDir(root); !errors.Is(err, ErrEntryLimit) {
		t.Fatalf("ReadDir error = %v, want ErrEntryLimit", err)
	}
}

func TestSourceReadsFileAtExactByteLimit(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "session_index.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("four"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenSource(home, Limits{MaxFileBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	file, err := source.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll error = %v", err)
	}
	if string(content) != "four" {
		t.Fatalf("content = %q, want %q", content, "four")
	}
}

func TestSelectionSinceMatchesSFTPLookback(t *testing.T) {
	since := (48 * time.Hour).Milliseconds()
	if got, want := selectionSince(since), (24 * time.Hour).Milliseconds(); got != want {
		t.Fatalf("selection since = %d, want %d", got, want)
	}
	if got := selectionSince((12 * time.Hour).Milliseconds()); got != 0 {
		t.Fatalf("selection since before epoch = %d, want 0", got)
	}
}

func TestSFTPCompatibleFingerprintUsesWholeSecondMtime(t *testing.T) {
	fingerprint := sftpCompatibleFingerprint(vendors.FileFingerprint{Key: "file", Size: 9, ModifiedAtMs: 1_729_123_456_789})
	if got, want := fingerprint.ModifiedAtMs, int64(1_729_123_456_000); got != want {
		t.Fatalf("fingerprint mtime = %d, want %d", got, want)
	}
}

func TestCodexScanReusesUnchangedCachedHeader(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	id := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	file := filepath.Join(root, "rollout-2026-07-10T14-11-18-"+id+".jsonl")
	// Deliberately not a valid Codex header. Successful attribution proves the
	// unchanged cached mapping was used instead of reopening the first row.
	if err := os.WriteFile(file, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenSource(home, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	fingerprints, err := vendors.FingerprintSourceFiles(source, root, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	fp := sftpCompatibleFingerprint(fingerprints[0])
	request := validRequest()
	request.Known = []remoteprotocol.KnownFamily{{
		Vendor: "codex", FamilyID: id, Fingerprint: "family-fingerprint",
		Headers: []remoteprotocol.KnownHeader{{
			Key: fp.Key, Size: fp.Size, ModifiedAtMs: fp.ModifiedAtMs, SessionID: id,
		}},
	}}
	scan := scanCodex(source, home, request, noCodexLiveSessions)
	item := scan.scan.families[id]
	if item == nil || item.skipReason != "" || len(item.headerMappings) != 1 {
		t.Fatalf("cached header was not reused: %#v", item)
	}
}

func TestEmitterReportsNegotiatedOutputLimit(t *testing.T) {
	request := validRequest()
	request.Limits.MaxRecords = 1
	var output discardWriter
	_, err := Collect(context.Background(), request, testOptions(t.TempDir()), &output)
	if !errors.Is(err, ErrRecordLimit) {
		t.Fatalf("Collect error = %v, want ErrRecordLimit", err)
	}
}

func TestEmitterReservesCompletionRecordsForRemainingVendors(t *testing.T) {
	request := validRequest()
	request.Vendors = []string{vendors.AgentClaude, vendors.AgentCodex}
	request.Limits.MaxRecords = 4
	var output bytes.Buffer
	emitter := newEmitter(&output, request)
	if err := emitter.handshake(); err != nil {
		t.Fatal(err)
	}
	reservation, err := completionReserve(emitter, vendors.AgentClaude, nil, nil, []string{vendors.AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	emitter.reservedBytes = reservation.bytes
	emitter.reservedRecords = reservation.records
	counts := remoteprotocol.Counts{}
	emitted, err := emitter.emitFamily(remoteprotocol.Record{
		Type: remoteprotocol.RecordUnchanged, Vendor: vendors.AgentClaude,
		FamilyID: "root", Fingerprint: "same",
	}, &counts)
	if err != nil {
		t.Fatal(err)
	}
	if emitted || emitter.records != 1 || emitter.budgetSkipped != 1 || counts.SkippedFamilies != 1 {
		t.Fatalf("family consumed completion slot: emitted=%v records=%d skipped=%d counts=%#v", emitted, emitter.records, emitter.budgetSkipped, counts)
	}
}

func TestFamilyFactsFailureIsInvalidData(t *testing.T) {
	_, err := familyFacts(&vendorScan{vendor: "codex", metadata: vendors.EmptySessionMetadata()}, &family{
		id: "root", sessionIDs: []string{"root"},
		fingerprints: []vendors.FileFingerprint{{Key: "opaque", Size: 1, ModifiedAtMs: 2}},
	}, []*vendors.ParsedSession{{Session: &session.Session{ID: "root"}}})
	if !errors.Is(err, vendors.ErrInvalidData) || boundedReason(err) != remotefacts.StaleReasonInvalidData {
		t.Fatalf("error = %v", err)
	}
}

func TestPublishCodexFamilyWithoutSourceIDOmitsFullRecord(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "sessions", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("row\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenSource(home, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	info, err := source.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sftpCompatibleFingerprint(vendors.FileFingerprint{Key: "opaque", Size: info.Size(), ModifiedAtMs: info.ModTime().UnixMilli()})
	item := &family{id: "root", files: []string{path}, sessionIDs: []string{"root"}, fingerprints: []vendors.FileFingerprint{fingerprint}, fingerprint: "new"}
	scanned := &vendorScan{
		vendor: vendors.AgentCodex, source: source, metadata: vendors.EmptySessionMetadata(),
		fileFacts: map[string]vendors.FileFingerprint{path: fingerprint},
	}
	request := validRequest()
	request.SourceID = ""
	var output bytes.Buffer
	emitter := newEmitter(&output, request)
	if err := emitter.handshake(); err != nil {
		t.Fatal(err)
	}
	counts := remoteprotocol.Counts{}
	parsed := []*vendors.ParsedSession{{Session: &session.Session{
		Agent: vendors.AgentCodex, ID: "root", StartedAt: 1, LastActivityTime: 2,
		Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1},
	}}}
	if _, err := publishFamily(emitter, request, scanned, item, parsed, map[string]string{"root": "old"}, &counts); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	var record remoteprotocol.Record
	if err := json.Unmarshal(lines[len(lines)-1], &record); err != nil {
		t.Fatal(err)
	}
	if record.Type != remoteprotocol.RecordChanged || len(record.FullRecords) != 0 || counts.SkippedFamilies != 0 {
		t.Fatalf("protocol-v1 changed record = %#v, counts=%#v", record, counts)
	}

	before := output.Len()
	emitter.reservedBytes = request.Limits.MaxResponseBytes - emitter.bytes
	if _, err := publishFamily(emitter, request, scanned, item, parsed, map[string]string{"root": "old"}, &counts); err != nil {
		t.Fatal(err)
	}
	if output.Len() != before || emitter.budgetSkipped != 1 || counts.SkippedFamilies != 1 {
		t.Fatalf("aggregate-budget result: bytes=%d budget_skipped=%d counts=%#v", output.Len()-before, emitter.budgetSkipped, counts)
	}
}

func TestPublishCodexFamilyOverRecordLimitEmitsStructuredSkip(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "sessions", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("row\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenSource(home, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	info, err := source.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sftpCompatibleFingerprint(vendors.FileFingerprint{Key: "opaque", Size: info.Size(), ModifiedAtMs: info.ModTime().UnixMilli()})
	item := &family{id: "root", files: []string{path}, sessionIDs: []string{"root"}, fingerprints: []vendors.FileFingerprint{fingerprint}, fingerprint: "new"}
	scanned := &vendorScan{
		vendor: vendors.AgentCodex, source: source, metadata: vendors.EmptySessionMetadata(),
		fileFacts: map[string]vendors.FileFingerprint{path: fingerprint},
	}
	large := strings.Repeat("x", 8<<10)
	parsed := []*vendors.ParsedSession{{Session: &session.Session{
		Agent: vendors.AgentCodex, ID: "root", StartedAt: 1, LastActivityTime: 2,
		Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{FirstPrompt: &large},
	}}}
	request := validRequest()
	request.SourceID = "r_0123456789abcdef"
	request.Limits.MaxRecordBytes = 2 << 10
	var output bytes.Buffer
	emitter := newEmitter(&output, request)
	if err := emitter.handshake(); err != nil {
		t.Fatal(err)
	}
	counts := remoteprotocol.Counts{}
	if _, err := publishFamily(emitter, request, scanned, item, parsed, map[string]string{"root": "old"}, &counts); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	var record remoteprotocol.Record
	if err := json.Unmarshal(lines[len(lines)-1], &record); err != nil {
		t.Fatal(err)
	}
	if record.Type != remoteprotocol.RecordSkipped || record.Reason != remotefacts.StaleReasonVendorBudgetExceeded || counts.SkippedFamilies != 1 {
		t.Fatalf("oversized family result = %#v counts=%#v", record, counts)
	}
}

func TestCollectWithSkippedFamilyWithholdsCompletion(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	id := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	path := filepath.Join(root, "rollout-2026-07-10T14-11-18-"+id+".jsonl")
	if err := os.WriteFile(path, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenSource(home, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, err := vendors.FingerprintSourceFiles(source, root, []string{path})
	source.Close()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sftpCompatibleFingerprint(fingerprints[0])
	request := validRequest()
	request.SourceID = "r_0123456789abcdef"
	request.Known = []remoteprotocol.KnownFamily{{
		Vendor: vendors.AgentCodex, FamilyID: id, Fingerprint: "different",
		Headers: []remoteprotocol.KnownHeader{{
			Key: fingerprint.Key, Size: fingerprint.Size, ModifiedAtMs: fingerprint.ModifiedAtMs, SessionID: id,
		}},
	}}
	var output bytes.Buffer
	outcome, err := Collect(context.Background(), request, testOptions(home), &output)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RequestComplete || strings.Contains(output.String(), `"type":"request_complete"`) || !strings.Contains(output.String(), `"type":"skipped_family"`) {
		t.Fatalf("skipped response was reported complete: outcome=%#v output=%s", outcome, output.String())
	}
}

func TestCollectWithUnknownScanSkippedFamilyWithholdsCompletion(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	id := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	path := filepath.Join(root, "rollout-2026-07-10T14-11-18-"+id+".jsonl")
	if err := os.WriteFile(path, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := validRequest()
	var output bytes.Buffer
	outcome, err := Collect(context.Background(), request, testOptions(home), &output)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RequestComplete || strings.Contains(output.String(), `"type":"request_complete"`) {
		t.Fatalf("unknown skipped family was reported complete: outcome=%#v output=%s", outcome, output.String())
	}
}

func TestCollectForkedRolloutReportsCompleteDistinctFamilies(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootID := "11111111-2222-3333-4444-555555555555"
	threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	rootFile := filepath.Join(root, "rollout-2026-07-10T14-11-18-"+rootID+".jsonl")
	forkFile := filepath.Join(root, "rollout-2026-07-10T14-12-18-"+rootID+"_"+threadID+".jsonl")
	rootContent := `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + rootID + `","session_id":"` + rootID + `","cwd":"/test/project"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:11:19Z","type":"event_msg","payload":{"type":"user_message","message":"root work"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:11:20Z","type":"event_msg","payload":{"type":"task_started"}}` + "\n"
	forkContent := `{"timestamp":"2026-07-10T14:12:18Z","type":"session_meta","payload":{"id":"` + rootID + `","session_id":"` + rootID + `","cwd":"/test/project"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:12:19Z","type":"event_msg","payload":{"type":"user_message","message":"fork work"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:12:20Z","type":"event_msg","payload":{"type":"task_started"}}` + "\n"
	for file, content := range map[string]string{rootFile: rootContent, forkFile: forkContent} {
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	request := validRequest()
	request.SourceID = "r_0123456789abcdef"
	var output bytes.Buffer
	outcome, err := Collect(context.Background(), request, testOptions(home), &output)
	if err != nil {
		t.Fatal(err)
	}
	changed := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		var record remoteprotocol.Record
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record.Type == remoteprotocol.RecordChanged {
			changed[record.FamilyID] = true
			if len(record.FullRecords) != 1 || record.FullRecords[0].Record.SessionID != record.FamilyID {
				t.Fatalf("changed family %q has mismatched full records: %#v", record.FamilyID, record.FullRecords)
			}
		}
		if record.Type == remoteprotocol.RecordSkipped {
			t.Fatalf("forked rollout was skipped: %#v", record)
		}
	}
	if !outcome.RequestComplete || len(changed) != 2 || !changed[rootID] || !changed[threadID] {
		t.Fatalf("forked collection incomplete: outcome=%#v changed=%#v output=%s", outcome, changed, output.String())
	}
}

func TestPageFamiliesWrapsAfterCursor(t *testing.T) {
	families := []*family{{id: "a"}, {id: "b"}, {id: "c"}, {id: "d"}}
	got := pageFamilies(families, "b")
	want := []string{"c", "d", "a", "b"}
	for index, item := range got {
		if item.id != want[index] {
			t.Fatalf("paged family %d = %q, want %q", index, item.id, want[index])
		}
	}
}

func validRequest() remoteprotocol.Request {
	return remoteprotocol.Request{
		RequestID: "req-1", Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1},
		Schema: remoteprotocol.VersionRange{Min: remotefacts.SchemaVersion, Max: remotefacts.SchemaVersion}, ParserVersion: vendors.ParserVersion,
		BaselineMode: remoteprotocol.BaselineKnown, BaselineID: "base-1",
		CollectedAtMs: time.Now().UnixMilli(), Vendors: []string{"codex"},
		Limits: remoteprotocol.Limits{MaxRecordBytes: remoteprotocol.MaxRecordBytes,
			MaxResponseBytes: remoteprotocol.MaxResponseBytes, MaxRecords: 100,
			MaxInventoryFamilies: remoteprotocol.MaxInventoryFamilies},
	}
}

func testOptions(home string) Options {
	return Options{Home: home, CodexLiveSessions: noCodexLiveSessions}
}

func noCodexLiveSessions() (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

type discardWriter struct{}

func (*discardWriter) Write(data []byte) (int, error) { return len(data), nil }
