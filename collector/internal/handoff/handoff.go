// Package handoff renders the canonical context passed between agent sessions.
package handoff

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func Build(value *session.Session) string {
	title := value.ID
	if value.Name != nil {
		title = *value.Name
	}
	goals, goalSource := sessionGoals(value)
	current := "—"
	if value.Summary != nil {
		current = *value.Summary
	}
	if value.Synthesis != nil && strings.TrimSpace(value.Synthesis.Outcome) != "" {
		current = strings.TrimSpace(value.Synthesis.Outcome)
	}
	var lines []string
	lines = append(lines, "# Handoff — "+title, "", "## Objective ("+goalSource+")")
	lines = appendListOrText(lines, goals)
	lines = append(lines, "", "## Current state", current)
	if value.Synthesis != nil && len(value.Synthesis.KeyDecisions) > 0 {
		lines = appendBullets(append(lines, "", "## Key decisions"), value.Synthesis.KeyDecisions)
	}
	lines = append(lines, "", "## Timeline")
	if len(value.Digest) == 0 {
		lines = append(lines, "- —")
	} else {
		for _, entry := range value.Digest {
			lines = append(lines, fmt.Sprintf("- [%s · turn %d] %s", entry.Category, entry.Turn, entry.Description))
			if answer := strings.TrimSpace(entry.Answer); answer != "" {
				lines = append(lines, "  - Answer: "+answer)
			}
		}
	}
	if len(value.FileEdits) > 0 {
		lines = append(lines, "", "## Files")
		for _, edit := range value.FileEdits {
			lines = append(lines, fmt.Sprintf("- %s (+%d/-%d)", edit.Path, edit.Additions, edit.Deletions))
		}
	}
	if len(value.Commits) > 0 {
		lines = appendBullets(append(lines, "", "## Commits"), value.Commits)
	}
	var next []string
	for _, todo := range value.Todos {
		if !todo.Done {
			next = append(next, todo.Text)
		}
	}
	if len(next) == 0 && value.Synthesis != nil && value.Synthesis.NextStep != "" {
		next = append(next, value.Synthesis.NextStep)
	}
	if len(next) > 0 {
		lines = appendBullets(append(lines, "", "## Next steps"), next)
	}

	costLabel := "Estimated cost at list API prices"
	if value.Agent == "opencode" {
		costLabel = "Recorded cost"
	}
	lines = append(lines, "", "## Environment",
		"- Vendor: "+vendorLabel(value.Agent),
		"- Repository: "+environment(value.Repository),
		"- Branch: "+environment(value.Branch),
		"- Working directory: "+environmentString(value.WorkingDirectory),
		"- Runtime: "+formatDuration(value.DurationMs),
		"- Tokens: "+formatTokens(value.Tokens),
		"- "+costLabel+": "+formatCost(value.Cost),
		fmt.Sprintf("- Errors: %d; subagents: %d", value.Errors, len(value.Subagents)),
	)
	return strings.Join(lines, "\n")
}

func sessionGoals(value *session.Session) ([]string, string) {
	if value.DeclaredGoal != nil && strings.TrimSpace(*value.DeclaredGoal) != "" {
		return []string{*value.DeclaredGoal}, "declared"
	}
	if value.Synthesis != nil {
		var goals []string
		for _, goal := range value.Synthesis.Goals {
			if strings.TrimSpace(goal) != "" {
				goals = append(goals, goal)
			}
		}
		if len(goals) > 0 {
			return goals, "inferred"
		}
	}
	if value.FirstPrompt != nil && strings.TrimSpace(*value.FirstPrompt) != "" {
		return []string{strings.TrimSpace(*value.FirstPrompt)}, "first prompt"
	}
	return []string{"—"}, "first prompt"
}

func appendListOrText(lines, values []string) []string {
	if len(values) == 1 {
		return append(lines, values[0])
	}
	return appendBullets(lines, values)
}

func appendBullets(lines, values []string) []string {
	for _, value := range values {
		lines = append(lines, "- "+value)
	}
	return lines
}

func environment(value *string) string {
	if value == nil {
		return "—"
	}
	return environmentString(*value)
}

func environmentString(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return strings.TrimSpace(value)
}

func vendorLabel(agent string) string {
	switch agent {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	default:
		return agent
	}
}

func formatDuration(value *int) string {
	if value == nil {
		return "—"
	}
	minutes := int(math.Round(float64(*value) / 60_000))
	if minutes < 1 {
		return "<1m"
	}
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh %02dm", minutes/60, minutes%60)
}

func formatTokens(tokens map[string]session.ModelTokens) string {
	if len(tokens) == 0 {
		return "—"
	}
	total := 0
	for _, value := range tokens {
		total += value.InputTokens + value.OutputTokens + value.CacheCreationInputTokens +
			value.CacheCreation1hInputTokens + value.CacheReadInputTokens
	}
	if total >= 1_000_000 {
		label := strconv.FormatFloat(float64(total)/1_000_000, 'f', 2, 64)
		return strings.TrimRight(strings.TrimRight(label, "0"), ".") + "M"
	}
	return fmt.Sprintf("%dk", int(math.Round(float64(total)/1000)))
}

func formatCost(value *float64) string {
	if value == nil {
		return "—"
	}
	if *value > 0 && *value < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("≈$%.2f", *value)
}
