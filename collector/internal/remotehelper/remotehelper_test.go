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
	scan := scanCodex(source, home, request)
	item := scan.scan.families[id]
	if item == nil || item.skipReason != "" || len(item.headerMappings) != 1 {
		t.Fatalf("cached header was not reused: %#v", item)
	}
}

func TestEmitterReportsNegotiatedOutputLimit(t *testing.T) {
	request := validRequest()
	request.Limits.MaxRecords = 1
	var output discardWriter
	_, err := Collect(context.Background(), request, Options{Home: t.TempDir()}, &output)
	if !errors.Is(err, ErrRecordLimit) {
		t.Fatalf("Collect error = %v, want ErrRecordLimit", err)
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
		Tokens: map[string]session.ModelTokens{},
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
	outcome, err := Collect(context.Background(), request, Options{Home: home}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RequestComplete || strings.Contains(output.String(), `"type":"request_complete"`) || !strings.Contains(output.String(), `"type":"skipped_family"`) {
		t.Fatalf("skipped response was reported complete: outcome=%#v output=%s", outcome, output.String())
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

type discardWriter struct{}

func (*discardWriter) Write(data []byte) (int, error) { return len(data), nil }
