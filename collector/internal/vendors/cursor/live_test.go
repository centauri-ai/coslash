package cursor

import (
	"strings"
	"testing"
)

func TestLiveIDsFromLSOFCanonicalizesIDs(t *testing.T) {
	id := "ABCDEFAB-CDEF-4ABC-8DEF-ABCDEFABCDEF"
	output := "n/Users/test/.cursor/chats/abc/" + id + "/store.db\n"

	got := liveCLIFromLSOF(output)
	if !got[strings.ToLower(id)] || got[id] {
		t.Fatalf("live IDs = %v, want only canonical lowercase ID", got)
	}
}
