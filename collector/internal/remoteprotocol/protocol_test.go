package remoteprotocol

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func request() Request {
	r, err := BuildRequest(Request{RequestID: "req-1", Protocol: VersionRange{1, 1}, Schema: VersionRange{remotefacts.SchemaVersion, remotefacts.SchemaVersion}, ParserVersion: "parser-v1", BaselineID: "base-1", SinceMs: 1, CollectedAtMs: 2, Vendors: []string{"codex"}, Limits: Limits{MaxRecordBytes: MaxRecordBytes, MaxResponseBytes: MaxResponseBytes, MaxRecords: 100, MaxInventoryFamilies: 100}}, []KnownFamily{})
	if err != nil {
		panic(err)
	}
	return r
}

func TestChangedFamilyWithMultipleLargeDisplaysFitsRecordLimit(t *testing.T) {
	large := strings.Repeat("x", 600<<10)
	family, err := remotefacts.FromParsed("codex", "root", "parser-v1", remotefacts.StateComplete, "", []*vendors.ParsedSession{
		{Session: &session.Session{ID: "root", StartedAt: 1, LastActivityTime: 2, Summary: &large}},
		{Session: &session.Session{ID: "child", StartedAt: 1, LastActivityTime: 2, Summary: &large}, ParentID: "root"},
	}, vendors.EmptySessionMetadata(), []vendors.FileFingerprint{{Key: "opaque", Size: 1, ModifiedAtMs: 2}})
	if err != nil {
		t.Fatal(err)
	}
	record := Record{Type: RecordChanged, ProtocolVersion: ProtocolVersion, RequestID: strings.Repeat("r", remotefacts.MaxIDBytes), Sequence: MaxRecords, Vendor: "codex", FamilyID: "root", Fingerprint: strings.Repeat("f", remotefacts.MaxIDBytes), Family: &family}
	if size := encodedSize(record); size > MaxRecordBytes {
		t.Fatalf("changed family record is %d bytes, limit is %d", size, MaxRecordBytes)
	}
}

func family() remotefacts.Family {
	return remotefacts.Family{SchemaVersion: remotefacts.SchemaVersion, ParserVersion: "parser-v1", Vendor: "codex", FamilyID: "root", State: "complete", Sessions: []remotefacts.Session{{ID: "root", StartedAtMs: 1, LastActivityAtMs: 2, Usage: []remotefacts.ModelUsage{}, Spawns: []remotefacts.Spawn{}, CommandLabels: []string{}}}, Metadata: remotefacts.Metadata{Names: []remotefacts.MetadataName{}, Live: []remotefacts.MetadataLive{}}, Fingerprints: []remotefacts.Fingerprint{{Key: "opaque", Size: 1, ModifiedAtMs: 2}}}
}

func handshake(r Request) Record {
	return Record{Type: RecordHandshake, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 1, BaselineID: r.BaselineID, SchemaVersion: remotefacts.SchemaVersion, ParserVersion: "parser-v1"}
}

func TestBuildRequestOverflowUsesNoBaselineWithoutPartialFingerprints(t *testing.T) {
	known := make([]KnownFamily, MaxKnownFamilies+1)
	for i := range known {
		known[i] = KnownFamily{Vendor: "codex", FamilyID: "f" + strings.Repeat("x", i%100), Fingerprint: "fp"}
	}
	r := request()
	got, err := BuildRequest(r, known)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaselineMode != BaselineNone || got.BaselineID != "" || len(got.Known) != 0 {
		t.Fatalf("overflow request = %#v", got)
	}
}

func TestBuildRequestDoesNotMutateKnownInput(t *testing.T) {
	known := []KnownFamily{{Vendor: "codex", FamilyID: "z", Fingerprint: "fp"}, {Vendor: "codex", FamilyID: "a", Fingerprint: "fp"}}
	original := append([]KnownFamily(nil), known...)
	if _, err := BuildRequest(request(), known); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(known, original) {
		t.Fatalf("BuildRequest mutated input: got %#v want %#v", known, original)
	}
}

func TestBuildRequestSortsHeaderMappingsAndFallsBackWhenTheyOverflow(t *testing.T) {
	known := []KnownFamily{{Vendor: "codex", FamilyID: "root", Fingerprint: "fp", Headers: []KnownHeader{
		{Key: "z", Size: 1, ModifiedAtMs: 1, SessionID: "z"},
		{Key: "a", Size: 1, ModifiedAtMs: 1, SessionID: "a"},
	}}}
	got, err := BuildRequest(request(), known)
	if err != nil {
		t.Fatal(err)
	}
	if got.Known[0].Headers[0].Key != "a" || known[0].Headers[0].Key != "z" {
		t.Fatalf("header ordering/input mutation: got %#v input %#v", got.Known, known)
	}
	known[0].Headers = make([]KnownHeader, MaxKnownHeaders+1)
	got, err = BuildRequest(request(), known)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaselineMode != BaselineNone || len(got.Known) != 0 {
		t.Fatalf("header overflow did not discard baseline: %#v", got)
	}
}

func TestDecodeRejectsUnknownFieldsAndTrailingContent(t *testing.T) {
	r := request()
	for _, input := range []string{
		`{"type":"handshake","protocol_version":1,"request_id":"req-1","sequence":1,"baseline_id":"base-1","schema_version":1,"parser_version":"parser-v1","secret":"x"}` + "\n",
		`{"type":"request_complete","protocol_version":1,"request_id":"req-1","sequence":1}` + "\n{}\n",
		`{"type":"handshake","protocol_version":1,"request_id":"req-1","sequence":1,"baseline_id":"base-1","schema_version":1,"parser_version":"parser-v1"}` + "\n",
	} {
		if _, err := Decode(strings.NewReader(input), r); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestInterruptedBeforeVendorCompletionCannotDelete(t *testing.T) {
	r := request()
	baseFamily := family()
	baseline := Generation{BaselineID: "base-1", Families: map[FamilyKey]CachedFamily{{"codex", "root"}: {Facts: baseFamily, Fingerprint: "old"}}}
	a, err := NewAccumulator(r, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(handshake(r)); err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(Record{Type: RecordTombstone, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Proposal().Families[FamilyKey{"codex", "root"}]; !ok {
		t.Fatal("provisional tombstone deleted cached family")
	}
}

func TestCompleteInventoryAuthorizesDeletion(t *testing.T) {
	r := request()
	baseline := Generation{BaselineID: "base-1", Families: map[FamilyKey]CachedFamily{{"codex", "root"}: {Facts: family(), Fingerprint: "old"}}}
	a, _ := NewAccumulator(r, baseline)
	_ = a.Apply(handshake(r))
	_ = a.Apply(Record{Type: RecordTombstone, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root"})
	if err := a.Apply(Record{Type: RecordVendorComplete, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 3, Vendor: "codex", EnumerationComplete: true, InventoryComplete: true, Inventory: []string{}}); err != nil {
		t.Fatal(err)
	}
	if len(a.Proposal().Families) != 0 {
		t.Fatal("authorized tombstone was not applied")
	}
}

func TestChangedFamilyPublishesBeforeRequestCompletionAndFailedReplacementStaysGood(t *testing.T) {
	r := request()
	baseline := Generation{BaselineID: "base-1", Families: map[FamilyKey]CachedFamily{{"codex", "old"}: {Facts: family(), Fingerprint: "old"}}}
	a, _ := NewAccumulator(r, baseline)
	_ = a.Apply(handshake(r))
	changed := family()
	if err := a.Apply(Record{Type: RecordChanged, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root", Fingerprint: "new", Family: &changed}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Proposal().Families[FamilyKey{"codex", "root"}]; !ok {
		t.Fatal("validated changed family not publishable")
	}
	if err := a.Apply(Record{Type: RecordSkipped, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 3, Vendor: "codex", FamilyID: "old", Reason: remotefacts.StaleReasonUnstableFile}); err != nil {
		t.Fatal(err)
	}
	if got := a.Proposal().Families[FamilyKey{"codex", "old"}]; got.Fingerprint != "old" || got.StaleReason != "unstable_file" {
		t.Fatalf("retained family = %#v", got)
	}
	if got := a.Proposal().Families[FamilyKey{"codex", "old"}].Facts.State; got != remotefacts.StateComplete {
		t.Fatalf("skip mutated last-good facts state to %q", got)
	}
}

func TestUnchangedFamilyClearsTransientStaleReason(t *testing.T) {
	r := request()
	baseline := Generation{BaselineID: "base-1", Families: map[FamilyKey]CachedFamily{{"codex", "root"}: {Facts: family(), Fingerprint: "old", StaleReason: "unstable_file"}}}
	a, err := NewAccumulator(r, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(handshake(r)); err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(Record{Type: RecordUnchanged, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root", Fingerprint: "old"}); err != nil {
		t.Fatal(err)
	}
	got := a.Proposal().Families[FamilyKey{"codex", "root"}]
	if got.StaleReason != "" || got.Facts.State != remotefacts.StateComplete {
		t.Fatalf("unchanged family did not recover last-good facts: %#v", got)
	}
}

func TestProposalAdvancesGenerationIdentity(t *testing.T) {
	r := request()
	a, err := NewAccumulator(r, Generation{BaselineID: "base-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Proposal().BaselineID; got != r.RequestID {
		t.Fatalf("proposal baseline_id = %q, want request_id %q", got, r.RequestID)
	}
}

func TestChangedFamilyRequiresConsistentPriorFingerprint(t *testing.T) {
	r := request()
	a, err := NewAccumulator(r, Generation{BaselineID: "base-1", Families: map[FamilyKey]CachedFamily{{"codex", "root"}: {Facts: family(), Fingerprint: "old"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(handshake(r)); err != nil {
		t.Fatal(err)
	}
	changed := family()
	if err := a.Apply(Record{Type: RecordChanged, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root", Fingerprint: "new", Family: &changed}); err == nil {
		t.Fatal("accepted changed family with missing prior fingerprint")
	}

	a, _ = NewAccumulator(r, Generation{BaselineID: "base-1", Families: map[FamilyKey]CachedFamily{}})
	_ = a.Apply(handshake(r))
	if err := a.Apply(Record{Type: RecordChanged, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root", PriorFingerprint: "old", Fingerprint: "new", Family: &changed}); err == nil {
		t.Fatal("accepted new family with prior fingerprint")
	}
}

func TestBaselineFreeChangedFamilyReplacesCachedFamilyWithoutPrior(t *testing.T) {
	r := request()
	r.BaselineMode, r.BaselineID, r.Known = BaselineNone, "", nil
	a, err := NewAccumulator(r, Generation{BaselineID: "base-1", Families: map[FamilyKey]CachedFamily{{"codex", "root"}: {Facts: family(), Fingerprint: "old"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(handshake(r)); err != nil {
		t.Fatal(err)
	}
	changed := family()
	record := Record{Type: RecordChanged, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root", Fingerprint: "new", Family: &changed}
	if err := a.Apply(record); err != nil {
		t.Fatal(err)
	}
	if got := a.Proposal().Families[FamilyKey{"codex", "root"}].Fingerprint; got != "new" {
		t.Fatalf("replacement fingerprint = %q, want new", got)
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	r := request()
	records := []Record{handshake(r), {Type: RecordRequestComplete, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2}}
	data, err := Encode(records)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(bytes.NewReader(data), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("records = %d", len(got))
	}
}

func TestSkippedFamilyRejectsUnstructuredReason(t *testing.T) {
	r := request()
	a, err := NewAccumulator(r, Generation{BaselineID: "base-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(handshake(r)); err != nil {
		t.Fatal(err)
	}
	err = a.Apply(Record{Type: RecordSkipped, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root", Reason: "raw stderr or transcript text"})
	if err == nil {
		t.Fatal("accepted an unstructured skip reason")
	}
}

func TestChangedFamilyRejectsStaleReasonOutsideStaleState(t *testing.T) {
	r := request()
	a, err := NewAccumulator(r, Generation{BaselineID: "base-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(handshake(r)); err != nil {
		t.Fatal(err)
	}
	changed := family()
	changed.StaleReason = "hostile transcript text"
	err = a.Apply(Record{Type: RecordChanged, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", FamilyID: "root", Fingerprint: "new", Family: &changed})
	if err == nil {
		t.Fatal("accepted a non-stale family carrying arbitrary stale reason text")
	}
}

func TestAccumulatorEnforcesRequestedVendorLifecycle(t *testing.T) {
	t.Run("unrequested vendor", func(t *testing.T) {
		r := request()
		a, _ := NewAccumulator(r, Generation{BaselineID: "base-1"})
		_ = a.Apply(handshake(r))
		record := Record{Type: RecordSkipped, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "claude", FamilyID: "root", Reason: remotefacts.StaleReasonInvalidData}
		if err := a.Apply(record); err == nil {
			t.Fatal("accepted record for unrequested vendor")
		}
	})

	t.Run("action after vendor complete", func(t *testing.T) {
		r := request()
		a, _ := NewAccumulator(r, Generation{BaselineID: "base-1"})
		_ = a.Apply(handshake(r))
		_ = a.Apply(Record{Type: RecordVendorComplete, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", EnumerationComplete: true, InventoryComplete: true, Inventory: []string{}})
		record := Record{Type: RecordSkipped, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 3, Vendor: "codex", FamilyID: "root", Reason: remotefacts.StaleReasonInvalidData}
		if err := a.Apply(record); err == nil {
			t.Fatal("accepted family action after vendor completion")
		}
	})

	t.Run("request complete before vendor complete", func(t *testing.T) {
		r := request()
		a, _ := NewAccumulator(r, Generation{BaselineID: "base-1"})
		_ = a.Apply(handshake(r))
		record := Record{Type: RecordRequestComplete, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2}
		if err := a.Apply(record); err == nil {
			t.Fatal("accepted request completion before vendor completion")
		}
	})
}

func TestCoverageMovesOnlyToNarrowerCompleteWindow(t *testing.T) {
	r := request()
	r.CollectedAtMs = 1000
	base := Generation{BaselineID: "base-1", CoverageSinceMs: 100, Families: map[FamilyKey]CachedFamily{}}
	for _, since := range []int64{200, 50} {
		r.SinceMs = since
		a, err := NewAccumulator(r, base)
		if err != nil {
			t.Fatal(err)
		}
		for sequence, record := range []Record{
			handshake(r),
			{Type: RecordVendorComplete, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 2, Vendor: "codex", EnumerationComplete: true, InventoryComplete: true, Inventory: []string{}},
			{Type: RecordRequestComplete, ProtocolVersion: 1, RequestID: r.RequestID, Sequence: 3},
		} {
			record.Sequence = sequence + 1
			if err := a.Apply(record); err != nil {
				t.Fatal(err)
			}
		}
		got := a.Proposal().CoverageSinceMs
		want := int64(100)
		if since == 50 {
			want = 50
		}
		if got != want {
			t.Fatalf("since %d coverage = %d, want %d", since, got, want)
		}
	}
}
