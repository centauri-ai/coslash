package session

import (
	"regexp"
	"strings"
)

// claude does not provide an API to get context window size or is
// it reported by the agent, so we will have to maintain this information
var contextWindows = map[string]int{
	"claude-fable-5":    1_000_000,
	"claude-mythos-5":   1_000_000,
	"claude-opus-5":     1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"claude-opus-4-6":   1_000_000,
	"claude-sonnet-5":   967_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-haiku-4-5":  200_000,
	// Codex/GPT self reports context window
}

var releaseDateSuffix = regexp.MustCompile(`-\d{8}$`)

func ContextWindowFor(model string) *int {
	if model == "" {
		return nil
	}
	if strings.Contains(model, "[1m]") {
		window := 1_000_000
		return &window
	}
	key := releaseDateSuffix.ReplaceAllString(model, "")
	if window, ok := contextWindows[key]; ok {
		return &window
	}
	return nil
}

func ContextTokens(inputTokens, cacheReadTokens, cacheWriteTokens int) int {
	return inputTokens + cacheReadTokens + cacheWriteTokens
}
