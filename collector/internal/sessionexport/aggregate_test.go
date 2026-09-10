package sessionexport

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

func TestMarshalDeterministicallyFitsLargeSafeEvidence(t *testing.T) {
	root := "/repo"
	repository := "github.com/centauri-ai/coslash"
	edits := make([]session.FileEdit, snapshotv1.MaxFileEditItems)
	for i := range edits {
		edits[i] = session.FileEdit{
			Path:  fmt.Sprintf("dir/%04d-%s", i, strings.Repeat("p", maxPathBytes-16)),
			Edits: 1,
		}
	}
	local := session.Session{
		Agent: "codex", ID: "large-safe-evidence", Repository: &repository, StartedAt: 1,
		WorkingDirectory: root, Tokens: map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{FileEdits: edits},
	}

	built, err := Build(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	size, err := snapshotv1.Size(built)
	if err != nil {
		t.Fatal(err)
	}
	if size <= snapshotv1.MaxPayloadBytes {
		t.Fatalf("test profile is only %d bytes; want over %d", size, snapshotv1.MaxPayloadBytes)
	}

	first, err := Marshal(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("aggregate fitting is not deterministic")
	}
	if len(first) > snapshotv1.MaxPayloadBytes {
		t.Fatalf("fitted snapshot is %d bytes", len(first))
	}
	decoded, err := snapshotv1.Decode(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Session.FileEdits) >= len(edits) {
		t.Fatalf("file edits were not reduced: %d", len(decoded.Session.FileEdits))
	}
	for _, item := range decoded.Truncation {
		if item.Path == "/session/fileEdits" && item.Reason == snapshotv1.TruncationReasonAggregateBudget {
			return
		}
	}
	t.Fatalf("aggregate reduction was not recorded: %#v", decoded.Truncation)
}
