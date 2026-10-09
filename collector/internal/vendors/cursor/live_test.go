//go:build !windows

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

func TestLiveIDEFromLSOFMatchesMacOSAndLinuxStores(t *testing.T) {
	mac := "0123abcd-0000-4000-8000-000000000001"
	linux := "0123abcd-0000-4000-8000-000000000002"
	output := "n/Users/test/Library/Application Support/Cursor/AgentStores/cursor_agent_stores/" + mac + "/.sync/index.sqlite\n" +
		"n/home/test/.config/Cursor/AgentStores/cursor_agent_stores/" + linux + "/.sync/index.sqlite-wal\n" +
		"n/home/test/.config/Cursor/AgentStores/cursor_agent_stores/" + linux + "/other/index.sqlite\n"

	got := liveIDEFromLSOF(output)
	if len(got) != 2 || !got[mac] || !got[linux] {
		t.Fatalf("live IDE IDs = %v, want the macOS and Linux sessions", got)
	}
}
