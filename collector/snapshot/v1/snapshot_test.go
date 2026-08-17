package snapshotv1

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestMarshalAndDecodeCanonicalSnapshot(t *testing.T) {
	snapshot := validSnapshot()
	first, err := Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("canonical bytes changed between identical marshals")
	}
	if bytes.HasSuffix(first, []byte("\n")) || bytes.Contains(first, []byte("  \"")) {
		t.Fatal("canonical JSON contains formatting whitespace")
	}
	decoded, err := Decode(first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(decoded.ContentHash, "sha256:") {
		t.Fatalf("missing frozen hash: %q", decoded.ContentHash)
	}
}

func TestDecodeRejectsUnknownFieldsBadHashAndNonCanonicalBytes(t *testing.T) {
	data, err := Marshal(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}

	unknown := bytes.Replace(data, []byte(`"schemaVersion"`), []byte(`"unknown":true,"schemaVersion"`), 1)
	if _, err := Decode(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}

	badHash := append([]byte(nil), data...)
	index := bytes.Index(badHash, []byte("sha256:")) + len("sha256:")
	badHash[index] = 'f'
	if data[index] == 'f' {
		badHash[index] = 'e'
	}
	if _, err := Decode(badHash); !errors.Is(err, ErrBadHash) {
		t.Fatalf("bad hash error = %v", err)
	}

	nonCanonical := append(append([]byte(nil), data...), '\n')
	if _, err := Decode(nonCanonical); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("non-canonical error = %v", err)
	}
}

func TestMarshalRejectsAggregateOverflow(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Session.FileEdits = make([]FileEdit, MaxFileEditItems)
	for i := range snapshot.Session.FileEdits {
		snapshot.Session.FileEdits[i] = FileEdit{Path: strings.Repeat("x", MaxPathBytes-8) + string(rune('a'+i%26)), Edits: 1}
	}
	_, err := Marshal(snapshot)
	if !errors.Is(err, ErrOversized) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestCostMicroUSDFreezesEstimate(t *testing.T) {
	got, err := CostMicroUSD(1.2345678)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1_234_568 {
		t.Fatalf("cost = %d", got)
	}
	if _, err := CostMicroUSD(-1); err == nil {
		t.Fatal("negative cost accepted")
	}
	if _, err := CostMicroUSD(float64(^uint64(0)>>1) / 1_000_000); err == nil {
		t.Fatal("overflow boundary accepted")
	}
}

func TestOptionalModelAndWorkingDirectoryMatchSchemaConstraints(t *testing.T) {
	empty := ""
	snapshot := validSnapshot()
	snapshot.Session.Model = &empty
	if _, err := Marshal(snapshot); err == nil {
		t.Fatal("empty session model accepted")
	}

	snapshot = validSnapshot()
	snapshot.Session.WorkingDirectory = &empty
	if _, err := Marshal(snapshot); err == nil {
		t.Fatal("empty working directory accepted")
	}
}

func TestAgentIdentifierIsExtensibleAndBounded(t *testing.T) {
	for _, agent := range []string{"codex", "future-agent", strings.Repeat("a", MaxAgentBytes)} {
		snapshot := validSnapshot()
		snapshot.Agent = agent
		if _, err := Marshal(snapshot); err != nil {
			t.Fatalf("agent %q rejected: %v", agent, err)
		}
	}
	for _, agent := range []string{"", "-future", "FutureAgent", "future/agent", strings.Repeat("a", MaxAgentBytes+1)} {
		snapshot := validSnapshot()
		snapshot.Agent = agent
		if _, err := Marshal(snapshot); err == nil {
			t.Fatalf("agent %q accepted", agent)
		}
	}
}

func TestMetadataPathsRequireJSONPointerEscaping(t *testing.T) {
	for _, path := range []string{"not-a-pointer", "/bad~", "/bad~2escape"} {
		snapshot := validSnapshot()
		snapshot.Redactions = []Redaction{{Path: path, Reason: "test"}}
		if _, err := Marshal(snapshot); err == nil {
			t.Fatalf("path %q accepted", path)
		}
	}
	snapshot := validSnapshot()
	snapshot.Redactions = []Redaction{{Path: "/session", Reason: "test"}}
	if _, err := Marshal(snapshot); err != nil {
		t.Fatalf("resolving pointer rejected: %v", err)
	}
	snapshot.Redactions = []Redaction{{Path: "/session/missing", Reason: "test"}}
	if _, err := Marshal(snapshot); err == nil {
		t.Fatal("non-resolving pointer accepted")
	}
}

func TestTruncationCountersMustBeNonNegative(t *testing.T) {
	negative := -1
	tests := []struct {
		name string
		set  func(*Truncation)
	}{
		{"original bytes", func(item *Truncation) { item.OriginalBytes = &negative }},
		{"exported bytes", func(item *Truncation) { item.ExportedBytes = &negative }},
		{"original items", func(item *Truncation) { item.OriginalItems = &negative }},
		{"exported items", func(item *Truncation) { item.ExportedItems = &negative }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := validSnapshot()
			item := Truncation{Path: "/session", Reason: TruncationReasonTextBudget}
			test.set(&item)
			snapshot.Truncation = []Truncation{item}
			if _, err := Marshal(snapshot); err == nil {
				t.Fatal("negative truncation counter accepted")
			}
		})
	}
}

func validSnapshot() Snapshot {
	return Snapshot{
		SchemaVersion:      SchemaVersion,
		MediaType:          MediaType,
		CollectorVersion:   "0.1.0",
		Agent:              "codex",
		SourceSessionID:    "session-1",
		SessionStartedAtMs: 1_700_000_000_000,
		Repository:         Repository{Canonical: "github.com/centauri-ai/coslash"},
		Truncation:         []Truncation{},
		Redactions:         []Redaction{},
		Session: Session{
			LastActivityAtMs: 1_700_000_001_000,
			Counts:           Counts{},
			Usage:            Usage{Models: []ModelUsage{}, UnpricedModels: []string{}},
			Digest:           []Digest{},
			Todos:            []Todo{},
			FileEdits:        []FileEdit{},
			Commits:          []string{},
			Subagents:        []Subagent{},
		},
	}
}
