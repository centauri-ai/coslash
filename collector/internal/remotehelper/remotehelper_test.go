package remotehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
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

func validRequest() remoteprotocol.Request {
	return remoteprotocol.Request{
		RequestID: "req-1", Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1},
		Schema: remoteprotocol.VersionRange{Min: 1, Max: 1}, ParserVersion: vendors.ParserVersion,
		BaselineMode: remoteprotocol.BaselineKnown, BaselineID: "base-1",
		CollectedAtMs: time.Now().UnixMilli(), Vendors: []string{"codex"},
		Limits: remoteprotocol.Limits{MaxRecordBytes: remoteprotocol.MaxRecordBytes,
			MaxResponseBytes: remoteprotocol.MaxResponseBytes, MaxRecords: 100,
			MaxInventoryFamilies: remoteprotocol.MaxInventoryFamilies},
	}
}

type discardWriter struct{}

func (*discardWriter) Write(data []byte) (int, error) { return len(data), nil }
