package remotehelper

import (
	"slices"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// An empty key is kept, not dropped, so remotefacts validation rejects the
// family instead of hiding the bad fingerprint.
func TestScanFinishDedupesSortedFingerprintsAndSessionIDs(t *testing.T) {
	s := newScan()
	for _, add := range []struct{ session, key string }{{"b", "k2"}, {"a", ""}, {"b", "k1"}, {"a", "k2"}, {"", ""}} {
		s.add("root", add.session, vendors.FileFingerprint{Key: add.key}, add.key, false)
	}
	s.finish(nil)
	item := s.families["root"]
	var keys []string
	for _, fingerprint := range item.fingerprints {
		keys = append(keys, fingerprint.Key)
	}
	if !slices.Equal(keys, []string{"", "k1", "k2"}) || !slices.Equal(item.sessionIDs, []string{"a", "b"}) {
		t.Fatalf("fingerprint keys = %q, session IDs = %q", keys, item.sessionIDs)
	}
}

func TestAggregateFingerprintIncludesSessionEnrichment(t *testing.T) {
	item := &family{id: "root", sessionIDs: []string{"root"}}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("root").Summary = "before"
	before := aggregateFingerprint(item, metadata)

	metadata.Session("root").Summary = "after"
	after := aggregateFingerprint(item, metadata)

	if before == after {
		t.Fatal("summary-only metadata change did not change family fingerprint")
	}
}
