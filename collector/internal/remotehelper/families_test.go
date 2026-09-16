package remotehelper

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

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
