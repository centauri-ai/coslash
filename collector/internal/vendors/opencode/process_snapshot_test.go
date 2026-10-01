package opencode

import (
	"strings"
	"testing"
)

func TestWindowsProcessSnapshotRequiresArray(t *testing.T) {
	for _, input := range []string{"null", "{}", "[", "[] trailing", "", strings.Repeat("x", (4<<20)+1)} {
		if _, err := decodeWindowsProcessSnapshot([]byte(input), true); err == nil {
			t.Fatalf("accepted unverifiable snapshot %q", input[:min(len(input), 20)])
		}
	}
	for _, input := range []string{"[]", " \n [] \t"} {
		if got, err := decodeWindowsProcessSnapshot([]byte(input), true); err != nil || len(got) != 0 {
			t.Fatalf("valid empty snapshot: %v", err)
		}
	}
	if got, err := decodeWindowsProcessSnapshot([]byte("null"), false); err != nil || len(got) != 0 {
		t.Fatal("ordinary collection lost best-effort null handling")
	}
}
