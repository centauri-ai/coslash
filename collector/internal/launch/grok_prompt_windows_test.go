package launch

import (
	"os"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestGrokWindowsPromptFileAndResume(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, prompt := range []string{"fix the bug\n'$(private)'", ""} {
		command, path, err := cliCommandWithPrompt(vendors.AgentGrok, "", NewSession, "private prior notes", prompt)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = removeHandoffFile(path) })
		if strings.Contains(command, "private prior notes") || strings.Contains(command, "$(private)") {
			t.Fatal("private context leaked into process arguments")
		}
		for _, flag := range []string{"--session-id", "--prompt-file", "--tools=", "dontAsk", "--no-subagents", "--max-turns", "--resume", "$LASTEXITCODE -ne 0", "finally", powerShellRemove(path)} {
			if !strings.Contains(command, flag) {
				t.Fatalf("missing %q in %q", flag, command)
			}
		}
		contents, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(contents), "private prior notes") || !strings.Contains(string(contents), prompt) || !strings.Contains(string(contents), "untrusted historical data") {
			t.Fatalf("staged context = %q, err = %v", contents, err)
		}
	}
	if _, _, err := interactivePromptCommand(vendors.AgentClaude, "claude", "notes", "task"); err == nil {
		t.Fatal("unsupported Windows relay enabled")
	}
}
