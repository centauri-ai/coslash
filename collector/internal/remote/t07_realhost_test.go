package remote

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
)

// TestT07RealHostSFTPAndHelperParity is deliberately opt-in: it reads the
// caller's existing remote agent data over SSH. It emits only aggregate counts
// and comparison results, never family IDs, paths, or transcript-derived facts.
//
// Run with:
//
//	COSLASH_T07_REAL_HOST=1 COSLASH_T07_SSH_ALIAS=<alias> \
//	  COSLASH_T07_HELPER_PATH='~/.coslash/helpers/<version>/coslash-helper' \
//	  go test -count=1 -run '^TestT07RealHostSFTPAndHelperParity$' ./internal/remote
func TestT07RealHostSFTPAndHelperParity(t *testing.T) {
	if os.Getenv("COSLASH_T07_REAL_HOST") != "1" {
		t.Skip("set COSLASH_T07_REAL_HOST=1 to run against an authorized SSH host")
	}
	alias, helperPath := os.Getenv("COSLASH_T07_SSH_ALIAS"), os.Getenv("COSLASH_T07_HELPER_PATH")
	if alias == "" || helperPath == "" {
		t.Fatal("COSLASH_T07_SSH_ALIAS and COSLASH_T07_HELPER_PATH are required")
	}

	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sftp, err := refreshIncrementalWithOpen(ctx, alias, now.Add(-7*24*time.Hour).UnixMilli(), now, CachedSnapshotV2{}, OpenSession)
	if err != nil {
		t.Fatal("SFTP collection failed")
	}
	helper, err := helperRefreshWithOpen(ctx, alias, now.Add(-7*24*time.Hour).UnixMilli(), now, CachedSnapshotV2{}, helperTarget{path: helperPath}, OpenOptions{})
	if err != nil {
		t.Fatal("helper collection failed")
	}
	if len(sftp.Failures) != 0 || len(helper.Failures) != 0 {
		t.Fatal("collection reported partial vendor coverage")
	}

	sftpFamilies := familyFactsByKey(sftp.Snapshot)
	helperFamilies := familyFactsByKey(helper.Snapshot)
	if !reflect.DeepEqual(sftpFamilies, helperFamilies) {
		t.Fatalf(
			"SFTP/helper normalized facts differ (sftp_families=%d helper_families=%d sftp_by_vendor=%v helper_by_vendor=%v sftp_only=%v helper_only=%v field_differences=%v)",
			len(sftpFamilies), len(helperFamilies), familyCountsByVendor(sftpFamilies), familyCountsByVendor(helperFamilies),
			familyDifferenceCounts(sftpFamilies, helperFamilies), familyDifferenceCounts(helperFamilies, sftpFamilies),
			familyFieldDifferenceCounts(sftpFamilies, helperFamilies),
		)
	}
	warm, err := helperRefreshWithOpen(ctx, alias, now.Add(-7*24*time.Hour).UnixMilli(), now, helper.Snapshot, helperTarget{path: helperPath}, OpenOptions{})
	if err != nil {
		t.Fatal("warm helper collection failed")
	}
	if len(warm.Failures) != 0 {
		t.Fatal("warm helper collection reported partial vendor coverage")
	}
	if !reflect.DeepEqual(helperFamilies, familyFactsByKey(warm.Snapshot)) {
		t.Fatal("warm helper collection changed normalized facts")
	}
	// An empty host has no family bodies to elide, and the known-baseline
	// envelope can be a few bytes larger than the baseline-free request. Only
	// require byte reduction when the initial response contained family facts.
	if len(helperFamilies) > 0 && warm.Metrics.ResponseBytes >= helper.Metrics.ResponseBytes {
		t.Fatalf("warm helper response was not smaller (initial_bytes=%d warm_bytes=%d)", helper.Metrics.ResponseBytes, warm.Metrics.ResponseBytes)
	}
	t.Logf("parity passed: families=%d sftp_round_trip_ms=%d helper_round_trip_ms=%d helper_response_bytes=%d warm_round_trip_ms=%d warm_response_bytes=%d",
		len(sftpFamilies), sftp.RoundTrip.Milliseconds(), helper.RoundTrip.Milliseconds(), helper.Metrics.ResponseBytes,
		warm.RoundTrip.Milliseconds(), warm.Metrics.ResponseBytes)
}

func familyFactsByKey(snapshot CachedSnapshotV2) map[string]remotefacts.Family {
	result := make(map[string]remotefacts.Family, len(snapshot.Families))
	for _, family := range snapshot.Families {
		result[family.Vendor+"\x00"+family.FamilyID] = family.Facts
	}
	return result
}

func familyCountsByVendor(families map[string]remotefacts.Family) map[string]int {
	counts := map[string]int{}
	for _, family := range families {
		counts[family.Vendor]++
	}
	return counts
}

func familyDifferenceCounts(left, right map[string]remotefacts.Family) map[string]int {
	counts := map[string]int{}
	for key, family := range left {
		if _, ok := right[key]; !ok {
			counts[family.Vendor]++
		}
	}
	return counts
}

// familyFieldDifferenceCounts deliberately reports only field classes and
// counts. It is used by the opt-in real-host test to diagnose parity without
// logging identifiers, paths, timestamps, or transcript-derived values.
func familyFieldDifferenceCounts(left, right map[string]remotefacts.Family) map[string]int {
	counts := map[string]int{}
	for key, l := range left {
		r, ok := right[key]
		if !ok {
			continue
		}
		for _, field := range []struct {
			name  string
			left  any
			right any
		}{
			{"schema_version", l.SchemaVersion, r.SchemaVersion},
			{"parser_version", l.ParserVersion, r.ParserVersion},
			{"state", l.State, r.State},
			{"stale_reason", l.StaleReason, r.StaleReason},
			{"sessions", l.Sessions, r.Sessions},
			{"metadata", l.Metadata, r.Metadata},
			{"fingerprints", l.Fingerprints, r.Fingerprints},
			{"header_mappings", l.HeaderMappings, r.HeaderMappings},
		} {
			if !reflect.DeepEqual(field.left, field.right) {
				counts[field.name]++
			}
		}
		for field, count := range fingerprintFieldDifferenceCounts(l.Fingerprints, r.Fingerprints) {
			counts["fingerprints."+field] += count
		}
	}
	return counts
}

func fingerprintFieldDifferenceCounts(left, right []remotefacts.Fingerprint) map[string]int {
	counts := map[string]int{}
	if len(left) != len(right) {
		counts["length"]++
	}
	byKey := make(map[string]remotefacts.Fingerprint, len(right))
	for _, item := range right {
		byKey[item.Key] = item
	}
	for _, item := range left {
		other, ok := byKey[item.Key]
		if !ok {
			counts["key_set"]++
			continue
		}
		if item.Size != other.Size {
			counts["size"]++
		}
		if item.ModifiedAtMs != other.ModifiedAtMs {
			counts["modified_at"]++
		}
	}
	return counts
}
