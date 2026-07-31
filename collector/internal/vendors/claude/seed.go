package claude

import (
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

var (
	compactionSection = regexp.MustCompile(`^\s*(?:#+\s*)?([1-9])\.\s+[^:]+:\s*$`)
	declaredGoal      = regexp.MustCompile(
		`(?s)<command-name>\s*/goal\s*</command-name>\s*<command-args>(.*?)</command-args>`,
	)
)

func stripCompactionSummary(text string) string {
	kept := map[string]bool{"1": true, "7": true, "8": true, "9": true}
	active := false
	sections := make([]string, 0, 4)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Continue the conversation") {
			break
		}
		if match := compactionSection.FindStringSubmatch(line); match != nil {
			active = kept[match[1]]
		}
		if active {
			sections = append(sections, line)
		}
	}
	return session.Truncate(strings.TrimSpace(strings.Join(sections, "\n")), 4_000)
}

func parseDeclaredGoal(text string) (string, bool) {
	if match := declaredGoal.FindStringSubmatch(text); match != nil {
		goal := strings.TrimSpace(match[1])
		return goal, goal != ""
	}
	if rest, ok := strings.CutPrefix(strings.TrimSpace(text), "/goal "); ok {
		goal := strings.TrimSpace(rest)
		return goal, goal != ""
	}
	return "", false
}
