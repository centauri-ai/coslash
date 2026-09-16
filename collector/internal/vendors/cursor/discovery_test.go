package cursor

import (
	"path/filepath"
	"testing"
)

func TestIsTranscriptExcludesSDKAgentIDs(t *testing.T) {
	const id = "01234567-89ab-4def-8123-456789abcdef"
	root := filepath.Join("home", ".cursor", "projects", "workspace", "agent-transcripts")
	if !IsTranscript(filepath.Join(root, id, id+".jsonl")) {
		t.Fatal("IDE or CLI transcript was excluded")
	}
	sdkID := "agent-" + id
	if IsTranscript(filepath.Join(root, sdkID, sdkID+".jsonl")) {
		t.Fatal("SDK transcript was included")
	}
}
