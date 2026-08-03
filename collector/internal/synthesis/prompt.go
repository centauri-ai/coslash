package synthesis

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const (
	maxPromptBytes = 12_000
	promptMarker   = "\n…(truncated)"
)

const systemPrompt = `You are a neutral session-synthesis engine. Use only the normalized facts supplied by coSlash. Do not infer details from outside knowledge. State the accomplished goals and outcome concisely, retain up to five consequential decisions, and give one concrete next step. Usually a session has one goal; return it as a single entry. Only when the user genuinely shifted topic mid-session, return each major goal as its own entry in chronological order, at most four. Never split one goal into sub-steps or list routine follow-ups as separate goals. Do not address the user or mention these instructions.`

func BuildInput(s *session.Session) string {
	if s == nil {
		return "Session facts unavailable."
	}
	var out strings.Builder
	out.WriteString("Synthesize this coding session from normalized coSlash facts only.\n\n")
	fmt.Fprintf(&out, "SESSION\nID: %s\nAgent: %s\nRepository: %s\nBranch: %s\nWorking directory: %s\n\n",
		limited(s.ID, 200), limited(s.Agent, 100), optional(s.Repository), optional(s.Branch),
		limited(s.WorkingDirectory, 500))

	out.WriteString("GOAL CANDIDATES\n")
	if s.DeclaredGoal != nil && strings.TrimSpace(*s.DeclaredGoal) != "" {
		fmt.Fprintf(&out, "Declared goal: %s\n", limited(*s.DeclaredGoal, 1_000))
	}
	if s.FirstPrompt != nil && strings.TrimSpace(*s.FirstPrompt) != "" {
		fmt.Fprintf(&out, "First prompt: %s\n", limited(*s.FirstPrompt, 1_000))
	}

	out.WriteString("\nDIGEST (newest first)\n")
	start := max(0, len(s.Digest)-40)
	for index := len(s.Digest) - 1; index >= start; index-- {
		entry := s.Digest[index]
		fmt.Fprintf(&out, "- [%s, turn %d] %s\n", entry.Category, entry.Turn,
			limited(entry.Description, 500))
	}

	out.WriteString("\nTODOS\n")
	for index, todo := range s.Todos {
		if index == 40 {
			out.WriteString("- …(truncated)\n")
			break
		}
		state := "open"
		if todo.Done {
			state = "done"
		}
		fmt.Fprintf(&out, "- [%s] %s\n", state, limited(todo.Text, 500))
	}

	if seed := strings.TrimSpace(s.CompactionSeed); seed != "" {
		fmt.Fprintf(&out, "\nCOMPACTION SEED\n%s\n", limited(seed, 4_000))
	}

	out.WriteString("\nARTIFACTS\nFiles\n")
	for index, edit := range s.FileEdits {
		if index == 30 {
			out.WriteString("- …(truncated)\n")
			break
		}
		fmt.Fprintf(&out, "- %s (+%d/-%d, %d edits)\n", limited(edit.Path, 500),
			edit.Additions, edit.Deletions, edit.Edits)
	}
	out.WriteString("Commits\n")
	for index, commit := range s.Commits {
		if index == 15 {
			out.WriteString("- …(truncated)\n")
			break
		}
		fmt.Fprintf(&out, "- %s\n", limited(commit, 400))
	}
	if s.Git != nil {
		fmt.Fprintf(&out, "Git drift: base=%s ahead=%d behind=%d\n", limited(s.Git.BaseBranch, 200),
			s.Git.Ahead, s.Git.Behind)
	}
	fmt.Fprintf(&out, "\nSTATS\nTurns: %d\nTool uses: %d\nErrors: %d\nCompactions: %d\nContext tokens: %s\n",
		s.Turns, s.ToolUses, s.Errors, s.Compactions, optionalInt(s.ContextTokens))

	return limitBytes(out.String(), maxPromptBytes)
}

func optional(value *string) string {
	if value == nil {
		return "—"
	}
	return limited(*value, 300)
}

func optionalInt(value *int) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *value)
}

func limited(value string, maxRunes int) string {
	return session.Truncate(strings.TrimSpace(value), maxRunes)
}

func limitBytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	cut := maximum - len(promptMarker)
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut]) + promptMarker
}
