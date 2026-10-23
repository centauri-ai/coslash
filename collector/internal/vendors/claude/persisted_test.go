package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const (
	persistedRoot = "11111111-1111-1111-1111-111111111111"
	persistedHome = "/Users/someone/.claude/projects/-work/"
)

func wrapper(path, preview string) string {
	return "<persisted-output>\nOutput too large (36.1KB). Full output saved to: " + path +
		"\n\nPreview (first 2KB):\n" + preview + "\n...\n</persisted-output>"
}

func jsonString(t *testing.T, text string) string {
	t.Helper()
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(text)
}

func persistedTranscript(t *testing.T, path string, results map[string]string) {
	t.Helper()
	rows := []string{`{"type":"user","timestamp":"2026-09-23T10:00:00Z","message":{"content":"go"}}`}
	for id := range results {
		rows = append(rows, bashUse(id, "cmd "+id))
	}
	for id, content := range results {
		rows = append(rows, bashResult(id, `"`+jsonString(t, content)+`"`, false))
	}
	writeOutputTranscript(t, path, strings.Join(rows, "\n")+"\n")
}

func TestPersistedOutputUsesFileInsideOwnToolResults(t *testing.T) {
	projects := t.TempDir()
	transcript := filepath.Join(projects, persistedRoot+".jsonl")
	full := strings.Repeat("head", 3000) + strings.Repeat("tail", 3000)
	writeOutputTranscript(t, filepath.Join(projects, persistedRoot, "tool-results", "big.txt"), full)
	persistedTranscript(t, transcript, map[string]string{
		// The home prefix differs from this machine; only the last three parts count.
		"a": wrapper(persistedHome+persistedRoot+"/tool-results/big.txt", "preview text"),
	})
	parsed, err := parseTranscript(transcript)
	if err != nil {
		t.Fatal(err)
	}
	got := parsed.Session.CommandOutputs
	if len(got) != 1 || got[0] == nil || *got[0] != session.TruncateOutput(full) || strings.Contains(*got[0], "preview text") {
		t.Fatalf("output len %d want %d", len(*got[0]), len(session.TruncateOutput(full)))
	}
	files, err := ToolResultFilesSourceContext(context.Background(), vendors.LocalReadSource, transcript)
	if err != nil || len(files) != 1 || files[0] != filepath.Join(projects, persistedRoot, "tool-results", "big.txt") {
		t.Fatalf("files = %v, err %v", files, err)
	}
}

func TestPersistedOutputFallsBackToPreviewWithoutPath(t *testing.T) {
	projects := t.TempDir()
	transcript := filepath.Join(projects, persistedRoot+".jsonl")
	outside := filepath.Join(projects, "secret.txt")
	writeOutputTranscript(t, outside, "SECRET")
	writeOutputTranscript(t, filepath.Join(projects, "other-session", "tool-results", "x.txt"), "OTHER")
	cases := map[string]string{
		"absent":       persistedHome + persistedRoot + "/tool-results/missing.txt",
		"other":        persistedHome + "other-session/tool-results/x.txt",
		"outside":      outside,
		"wrong-dir":    persistedHome + persistedRoot + "/elsewhere/big.txt",
		"traversal":    persistedHome + persistedRoot + "/tool-results/../../secret.txt",
		"backslash":    `C:\Users\x\.claude\projects\-work\` + persistedRoot + `\tool-results\missing.txt`,
		"relative-dir": "tool-results/big.txt",
	}
	results := map[string]string{}
	for id, path := range cases {
		results[id] = wrapper(path, "preview of "+id)
	}
	persistedTranscript(t, transcript, results)
	parsed, err := parseTranscript(transcript)
	if err != nil {
		t.Fatal(err)
	}
	for i, command := range parsed.Session.Commands {
		id := strings.TrimPrefix(command, "cmd ")
		output := parsed.Session.CommandOutputs[i]
		if output == nil || *output != "preview of "+id+"\n..." {
			t.Fatalf("%s output = %v", id, output)
		}
		if strings.Contains(*output, "/") || strings.Contains(*output, "SECRET") || strings.Contains(*output, "OTHER") {
			t.Fatalf("%s output leaks: %q", id, *output)
		}
	}
	files, err := ToolResultFilesSourceContext(context.Background(), vendors.LocalReadSource, transcript)
	if err != nil || len(files) != 1 {
		// absent and backslash name the same file inside the session directory; the rest are refused
		t.Fatalf("files = %v, err %v", files, err)
	}
}

func TestPersistedOutputOfSubagentUsesParentSessionDirectory(t *testing.T) {
	projects := t.TempDir()
	transcript := filepath.Join(projects, persistedRoot, "subagents", "agent-a1.jsonl")
	writeOutputTranscript(t, filepath.Join(projects, persistedRoot, "tool-results", "sub.txt"), "subagent full output\n")
	persistedTranscript(t, transcript, map[string]string{
		"s": wrapper(persistedHome+persistedRoot+"/tool-results/sub.txt", "preview"),
	})
	parsed, err := parseTranscript(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Commands; len(got) != 1 || got[0].Output == nil || *got[0].Output != "subagent full output\n" {
		t.Fatalf("subagent commands = %+v", got)
	}
}

func TestPersistedOutputIgnoresNonCommandAndPlainResults(t *testing.T) {
	text := "<persisted-output> is quoted in a normal output"
	if path, _, ok := persistedOutput(text); ok {
		t.Fatalf("plain text parsed as wrapper: %q", path)
	}
	if got, resolved := commandOutput(vendors.LocalReadSource, "x.jsonl", text); got != text || !resolved {
		t.Fatal("plain output changed")
	}
}

func TestPersistedOutputNeverKeepsPathOfUnknownWrapperShape(t *testing.T) {
	projects := t.TempDir()
	transcript := filepath.Join(projects, persistedRoot+".jsonl")
	shapes := map[string]string{
		"trailing":   wrapper(persistedHome+persistedRoot+"/tool-results/gone.txt", "kept preview") + "\ntrailing text",
		"reworded":   "<persisted-output>\nToo big. Full output saved to: /Users/someone/x/tool-results/gone.txt\nbody line\n</persisted-output>",
		"no-closing": "<persisted-output>\nOutput too large (1KB). Full output saved to: " + persistedHome + persistedRoot + "/tool-results/gone.txt\n\nPreview (first 2KB):\nkept preview",
	}
	persistedTranscript(t, transcript, shapes)
	parsed, err := parseTranscript(transcript)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range parsed.Session.CommandOutputs {
		if output == nil || strings.Contains(*output, "/Users/") || strings.Contains(*output, "Full output saved") {
			t.Fatalf("output keeps the path: %v", output)
		}
	}
}

func TestPersistedOutputRefusesSymbolicLink(t *testing.T) {
	projects := t.TempDir()
	transcript := filepath.Join(projects, persistedRoot+".jsonl")
	secret := filepath.Join(projects, "secret.txt")
	writeOutputTranscript(t, secret, "SECRET")
	link := filepath.Join(projects, persistedRoot, "tool-results", "link.txt")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("symbolic links are not available:", err)
	}
	persistedTranscript(t, transcript, map[string]string{"a": wrapper(persistedHome+persistedRoot+"/tool-results/link.txt", "preview only")})
	parsed, err := parseTranscript(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Session.CommandOutputs[0]; got == nil || *got != "preview only\n..." {
		t.Fatalf("output = %v", got)
	}
}

func TestPersistedOutputRefusesSymbolicLinkedDirectory(t *testing.T) {
	projects := t.TempDir()
	transcript := filepath.Join(projects, persistedRoot+".jsonl")
	elsewhere := filepath.Join(projects, "other-session", "tool-results")
	writeOutputTranscript(t, filepath.Join(elsewhere, "x.txt"), "OTHER")
	if err := os.MkdirAll(filepath.Join(projects, persistedRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(projects, persistedRoot, "tool-results")); err != nil {
		t.Skip("symbolic links are not available:", err)
	}
	persistedTranscript(t, transcript, map[string]string{"a": wrapper(persistedHome+persistedRoot+"/tool-results/x.txt", "preview only")})
	parsed, err := parseTranscript(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Session.CommandOutputs[0]; got == nil || *got != "preview only\n..." {
		t.Fatalf("output = %v", got)
	}
}
