package agentexec

import (
	"slices"
	"strings"
)

// sessionMarkers are the variables that a running Claude Code or Codex session
// sets for its own child processes. An agent that inherits them acts as a child
// of that session: Claude Code, for example, stops saving its transcript.
// Configuration and credentials such as CLAUDE_CODE_USE_FOUNDRY and CODEX_HOME
// share these prefixes, so the list names each marker exactly.
var sessionMarkers = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_EFFORT",
	"CLAUDE_PID",
	"CODEX_CI",
	"CODEX_SANDBOX",
	"CODEX_SANDBOX_NETWORK_DISABLED",
	"CODEX_SESSION_ID",
	"CODEX_THREAD_ID",
	"CODEX_VERSION",
}

// SessionMarkers returns the names that WithoutSessionMarkers removes.
func SessionMarkers() []string {
	return slices.Clone(sessionMarkers)
}

// WithoutSessionMarkers returns a copy of env, in KEY=VALUE form, without the
// agent session markers.
func WithoutSessionMarkers(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains(sessionMarkers, name) {
			kept = append(kept, entry)
		}
	}
	return kept
}
