package remote

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

func TestRemoteParserVersionsTrackVendorParsers(t *testing.T) {
	for _, item := range []struct{ remote, vendor string }{
		{claudeParserVersion, claude.ParserVersion},
		{codexParserVersion, codex.ParserVersion},
	} {
		if !strings.Contains(item.remote, vendors.ParserVersion) || !strings.Contains(item.remote, item.vendor) {
			t.Errorf("remote parser version %q ignores %q or %q", item.remote, vendors.ParserVersion, item.vendor)
		}
	}
}

func TestBaselineFromOtherParserVersionIsNeverKnown(t *testing.T) {
	claudeSnapshot := CachedSnapshotV2{Families: []CachedFamilyV2{{
		Vendor: vendors.AgentClaude, FamilyID: "root-1", Fingerprint: "fp-1", Facts: validFamily(t, "root-1"),
	}}}
	for _, snapshot := range []CachedSnapshotV2{claudeSnapshot, completeCodexSnapshot(t, "generation-1", "body\n")} {
		vendor := snapshot.Families[0].Vendor
		for version, wantKnown := range map[string]bool{
			remotefacts.ParserVersion(vendor): true,
			vendors.ParserVersion:             false,
		} {
			snapshot.Families[0].Facts.ParserVersion = version
			known := knownFamiliesFor(snapshotOrEmpty(&snapshot))
			if len(known) != 1 || (known[0].Fingerprint == "fp-1") != wantKnown {
				t.Errorf("%s facts from parser version %q: known = %#v, want reuse %v", vendor, version, known, wantKnown)
			}
		}
	}
}
