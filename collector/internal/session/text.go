package session

import (
	"regexp"
	"strings"
)

// harness injected prompt patterns
var harnessWrapper = regexp.MustCompile(
	`^<(?:local-command-(?:stdout|caveat)` +
		`|command-(?:name|message)` +
		`|system-reminder` +
		`|bash-(?:input|stdout)` +
		`|task-notification)>`,
)

func IsHarnessWrapped(text string) bool {
	return harnessWrapper.MatchString(text)
}

func Truncate(text string, max int) string {
	splitText := strings.Fields(text)
	collapsedText := strings.Join(splitText, " ")
	runes := []rune(collapsedText)
	if len(runes) <= max {
		return collapsedText
	}
	return string(runes[:max-1]) + "…"
}
