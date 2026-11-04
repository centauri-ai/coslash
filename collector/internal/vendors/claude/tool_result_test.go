package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func writeToolResultTranscript(t *testing.T, results ...string) string {
	t.Helper()
	rows := []string{`{"type":"user","timestamp":"2026-11-03T04:29:00Z","message":{"content":"Publish the notes"}}`}
	for _, result := range results {
		rows = append(rows, `{"type":"user","timestamp":"2026-11-03T04:29:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"Done."}]},"toolUseResult":`+result+`}`)
	}
	path := filepath.Join(t.TempDir(), "11111111-1111-1111-1111-111111111111.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestArtifactResultTypeObjectIsValidAndNotAnEdit(t *testing.T) {
	path := writeToolResultTranscript(t,
		`{"type":{"url":"https://example.test/type","release":"r1","latest":"r1"},"url":"https://example.test/doc","path":"doc.html"}`,
		`{"type":"create","filePath":"/repo/notes.md","content":"one\ntwo\n","structuredPatch":[]}`,
	)
	if err := ValidateCompleteTranscriptSourceContext(t.Context(), vendors.LocalReadSource, path); err != nil {
		t.Fatalf("validate = %v", err)
	}
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	edits := parsed.Session.FileEdits
	if len(edits) != 1 || edits[0].Path != "/repo/notes.md" || !edits[0].IsNew || edits[0].Additions != 2 {
		t.Fatalf("file edits = %#v, want only the created notes.md", edits)
	}
}

func TestMistypedToolResultFieldStillInvalid(t *testing.T) {
	path := writeToolResultTranscript(t, `{"type":"update","filePath":7}`)
	if err := ValidateCompleteTranscriptSourceContext(t.Context(), vendors.LocalReadSource, path); !errors.Is(err, vendors.ErrInvalidData) {
		t.Fatalf("validate = %v, want ErrInvalidData", err)
	}
}

func TestUnsupportedToolResultTypeShapesAreInvalid(t *testing.T) {
	for _, kind := range []string{`1`, `true`, `[]`, `null`} {
		t.Run(kind, func(t *testing.T) {
			path := writeToolResultTranscript(t, `{"type":`+kind+`,"filePath":"/repo/notes.md","content":"ignored"}`)
			if err := ValidateCompleteTranscriptSourceContext(t.Context(), vendors.LocalReadSource, path); !errors.Is(err, vendors.ErrInvalidData) {
				t.Fatalf("validate = %v, want ErrInvalidData", err)
			}
		})
	}
}
