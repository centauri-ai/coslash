package collector

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

type RemoteViewOptions struct {
	CollectorVersion string
	Capabilities     []string
	LaunchableAgents []string
	RequestedSinceMs int64
	RequestNowMs     int64
	HostNowMs        int64
	CollectedAtMs    int64
	HostOS           string
	HostArch         string
	Truncated        bool
	TruncationReason string
}

func BuildRemoteView(locals []*session.Session, options RemoteViewOptions) (remoteviewv1.View, error) {
	if options.RequestedSinceMs < 0 || options.RequestNowMs < 0 || options.RequestedSinceMs > options.RequestNowMs {
		return remoteviewv1.View{}, fmt.Errorf("invalid Mac request window")
	}
	view := remoteviewv1.View{
		SchemaVersion:    remoteviewv1.SchemaVersion,
		CollectorVersion: options.CollectorVersion,
		Capabilities:     cloneOrDefault(options.Capabilities, remoteviewv1.CapabilityRemoteView),
		LaunchableAgents: cloneOrEmpty(options.LaunchableAgents),
		RequestedSinceMs: options.RequestedSinceMs,
		RequestNowMs:     options.RequestNowMs,
		HostNowMs:        options.HostNowMs,
		CollectedAtMs:    options.CollectedAtMs,
		CoverageSinceMs:  options.RequestedSinceMs,
		Truncated:        options.Truncated,
		Host: remoteviewv1.Host{
			OS:   options.HostOS,
			Arch: options.HostArch,
		},
		Sessions: make([]remoteviewv1.Session, 0, len(locals)),
	}
	if options.Truncated {
		reason := options.TruncationReason
		if reason == "" {
			reason = remoteviewv1.TruncationReasonSession
		}
		view.TruncationReason = &reason
	}
	for _, local := range locals {
		if local == nil {
			continue
		}
		mapped, err := mapRemoteSession(*local)
		if err != nil {
			return remoteviewv1.View{}, err
		}
		view.Sessions = append(view.Sessions, mapped)
	}
	return remoteviewv1.FitView(view)
}

func BuildRemoteProbe(options RemoteViewOptions) (remoteviewv1.Probe, error) {
	probe := remoteviewv1.Probe{
		SchemaVersion:    remoteviewv1.SchemaVersion,
		CollectorVersion: options.CollectorVersion,
		Capabilities:     cloneOrDefault(options.Capabilities, remoteviewv1.CapabilityRemoteView),
		LaunchableAgents: cloneOrEmpty(options.LaunchableAgents),
		HostNowMs:        options.HostNowMs,
		Host: remoteviewv1.Host{
			OS:   options.HostOS,
			Arch: options.HostArch,
		},
	}
	if err := remoteviewv1.ValidateProbe(probe); err != nil {
		return remoteviewv1.Probe{}, err
	}
	return probe, nil
}

func cloneOrDefault(values []string, fallback string) []string {
	if values == nil {
		return []string{fallback}
	}
	return append([]string{}, values...)
}

func cloneOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}

func mapRemoteSession(local session.Session) (remoteviewv1.Session, error) {
	if local.Agent != remoteviewv1.AgentClaude && local.Agent != remoteviewv1.AgentCodex {
		return remoteviewv1.Session{}, fmt.Errorf("unsupported remote agent %q", local.Agent)
	}
	usage, err := mapUsage(local.Tokens, local.Cost, local.UnpricedModels)
	if err != nil {
		return remoteviewv1.Session{}, err
	}
	subagents, err := mapSubagents(local.Subagents)
	if err != nil {
		return remoteviewv1.Session{}, err
	}
	started := local.StartedAt
	activity := max(local.LastActivityTime, started)
	if started <= 0 {
		started = activity
	}
	if started <= 0 {
		return remoteviewv1.Session{}, fmt.Errorf("session %s has no positive start time", local.ID)
	}
	mapped := remoteviewv1.Session{
		Agent:               local.Agent,
		SourceSessionID:     boundRequired(local.ID, remoteviewv1.MaxIdentifierBytes),
		Name:                optionalBound(local.Name, remoteviewv1.MaxNameBytes),
		Summary:             optionalBound(local.Summary, remoteviewv1.MaxSummaryBytes),
		Status:              optionalBound(local.Status, 64),
		WorkingDirectory:    optionalPath(local.WorkingDirectory),
		Repository:          optionalBound(local.Repository, remoteviewv1.MaxRepositoryBytes),
		RepositoryLocalOnly: local.RepositoryLocalOnly,
		Branch:              optionalBound(local.Branch, remoteviewv1.MaxBranchBytes),
		Entrypoint:          optionalBound(local.Entrypoint, remoteviewv1.MaxEntrypointBytes),
		SessionStartedAtMs:  started,
		LastActivityAtMs:    activity,
		LastEditAtMs:        copyInt64(local.LastEditAt),
		DurationMs:          copyInt(local.DurationMs),
		Model:               optionalBound(local.Model, remoteviewv1.MaxModelBytes),
		ContextTokens:       copyInt(local.ContextTokens),
		ContextWindow:       copyInt(local.ContextWindow),
		DeclaredGoal:        optionalBound(local.DeclaredGoal, remoteviewv1.MaxGoalBytes),
		FirstPrompt:         optionalBound(local.FirstPrompt, remoteviewv1.MaxPromptBytes),
		Counts: remoteviewv1.Counts{
			EditedFiles:  local.EditedFileCount,
			Turns:        local.Turns,
			ToolUses:     local.ToolUses,
			Errors:       local.Errors,
			Compactions:  local.Compactions,
			Commands:     len(local.Commands),
			PullRequests: local.PullRequests,
		},
		Usage:     usage,
		Digest:    mapDigest(local.Digest),
		Todos:     mapTodos(local.Todos),
		FileEdits: mapFileEdits(local.FileEdits),
		Commits:   mapCommits(local.Commits),
		Git:       mapGit(local.Git),
		Subagents: subagents,
	}
	return mapped, nil
}

func mapUsage(tokens map[string]session.ModelTokens, cost float64, unpriced []string) (remoteviewv1.Usage, error) {
	estimated, err := remoteviewv1.CostMicroUSD(cost)
	if err != nil {
		return remoteviewv1.Usage{}, err
	}
	models := make([]remoteviewv1.ModelUsage, 0, len(tokens))
	for model, value := range tokens {
		boundedModel := boundRequired(model, remoteviewv1.MaxModelBytes)
		modelCost, err := remoteviewv1.CostMicroUSD(value.Cost)
		if err != nil {
			return remoteviewv1.Usage{}, err
		}
		models = append(models, remoteviewv1.ModelUsage{
			Model:                      boundedModel,
			InputTokens:                value.InputTokens,
			OutputTokens:               value.OutputTokens,
			CacheCreationInputTokens:   value.CacheCreationInputTokens,
			CacheCreation1hInputTokens: value.CacheCreation1hInputTokens,
			CacheReadInputTokens:       value.CacheReadInputTokens,
			EstimatedCostMicroUSD:      modelCost,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
	if len(models) > remoteviewv1.MaxUsageModels {
		models = models[:remoteviewv1.MaxUsageModels]
	}
	boundedUnpriced := uniqueBounded(unpriced, remoteviewv1.MaxModelBytes)
	if len(boundedUnpriced) > remoteviewv1.MaxUnpricedModels {
		boundedUnpriced = boundedUnpriced[:remoteviewv1.MaxUnpricedModels]
	}
	return remoteviewv1.Usage{
		Models:                models,
		EstimatedCostMicroUSD: estimated,
		UnpricedModels:        boundedUnpriced,
	}, nil
}

func mapDigest(entries []session.DigestEntry) []remoteviewv1.Digest {
	limit := min(len(entries), remoteviewv1.MaxDigestItems)
	out := make([]remoteviewv1.Digest, 0, limit)
	for _, entry := range entries[:limit] {
		item := remoteviewv1.Digest{
			Turn:        entry.Turn,
			Category:    boundRequired(entry.Category, 64),
			Description: boundText(entry.Description, remoteviewv1.MaxDigestTextBytes),
		}
		if entry.Answer != "" {
			answer := boundText(entry.Answer, remoteviewv1.MaxDigestTextBytes)
			item.Answer = &answer
		}
		if entry.SubagentID != "" {
			id := boundRequired(entry.SubagentID, remoteviewv1.MaxIdentifierBytes)
			item.SubagentID = &id
		}
		out = append(out, item)
	}
	return out
}

func mapTodos(todos []session.Todo) []remoteviewv1.Todo {
	limit := min(len(todos), remoteviewv1.MaxTodoItems)
	out := make([]remoteviewv1.Todo, 0, limit)
	for _, todo := range todos[:limit] {
		out = append(out, remoteviewv1.Todo{
			Text: boundText(todo.Text, remoteviewv1.MaxTodoTextBytes),
			Done: todo.Done,
		})
	}
	return out
}

func mapFileEdits(edits []session.FileEdit) []remoteviewv1.FileEdit {
	limit := min(len(edits), remoteviewv1.MaxFileEditItems)
	out := make([]remoteviewv1.FileEdit, 0, limit)
	for _, edit := range edits[:limit] {
		path := boundText(edit.Path, remoteviewv1.MaxPathBytes)
		if path == "" {
			continue
		}
		out = append(out, remoteviewv1.FileEdit{
			Path:      path,
			Additions: edit.Additions,
			Deletions: edit.Deletions,
			Edits:     edit.Edits,
			IsNew:     edit.IsNew,
		})
	}
	return out
}

func mapCommits(commits []string) []string {
	limit := min(len(commits), remoteviewv1.MaxCommitItems)
	out := make([]string, 0, limit)
	for _, commit := range commits[:limit] {
		out = append(out, boundText(commit, remoteviewv1.MaxCommitTextBytes))
	}
	return out
}

func mapGit(git *session.GitDrift) *remoteviewv1.GitDrift {
	if git == nil || git.BaseBranch == "" {
		return nil
	}
	return &remoteviewv1.GitDrift{
		BaseBranch: boundRequired(git.BaseBranch, remoteviewv1.MaxBranchBytes),
		Ahead:      git.Ahead,
		Behind:     git.Behind,
	}
}

func mapSubagents(subagents []session.Subagent) ([]remoteviewv1.Subagent, error) {
	limit := min(len(subagents), remoteviewv1.MaxSubagentItems)
	out := make([]remoteviewv1.Subagent, 0, limit)
	commandLabels := 0
	for _, subagent := range subagents[:limit] {
		usage, err := mapUsage(subagent.Tokens, subagent.Cost, nil)
		if err != nil {
			return nil, err
		}
		labels := make([]string, 0, len(subagent.Commands))
		for _, command := range subagent.Commands {
			if commandLabels >= remoteviewv1.MaxCommandLabelItems {
				break
			}
			label := boundRequired(command.Label, remoteviewv1.MaxCommandLabelBytes)
			if label == "" {
				continue
			}
			labels = append(labels, label)
			commandLabels++
		}
		out = append(out, remoteviewv1.Subagent{
			ID:                    boundRequired(subagent.ID, remoteviewv1.MaxIdentifierBytes),
			Name:                  boundRequired(subagent.Name, remoteviewv1.MaxNameBytes),
			Model:                 optionalBound(subagent.Model, remoteviewv1.MaxModelBytes),
			Status:                boundRequired(subagent.Status, 64),
			Task:                  boundText(subagent.Task, remoteviewv1.MaxSubagentTextBytes),
			Result:                boundText(subagent.Result, remoteviewv1.MaxSubagentTextBytes),
			DurationMs:            copyInt(subagent.DurationMs),
			SpawnedAtTurn:         copyInt(subagent.SpawnedAtTurn),
			ToolUses:              subagent.ToolUses,
			CommandLabels:         labels,
			Usage:                 usage.Models,
			EstimatedCostMicroUSD: usage.EstimatedCostMicroUSD,
		})
	}
	return out, nil
}

var credentialPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// Keep in sync with sessionexport; duplicated so P1 does not edit that package.
	{regexp.MustCompile(`(?i)("authorization"\s*:\s*"bearer\s+)(?:\\.|[^"\\])*(")`), `${1}[REDACTED]${2}`},
	{regexp.MustCompile(`(?i)("(?:password|passwd|token|secret|api[_-]?key)"\s*:\s*")(?:\\.|[^"\\])*(")`), `${1}[REDACTED]${2}`},
	{regexp.MustCompile(`(?i)(\bauthorization\b\s*[:=]\s*"bearer\s+)(?:\\.|[^"\\])*(")`), `${1}[REDACTED]${2}`},
	{regexp.MustCompile(`(?i)(\bauthorization\b\s*[:=]\s*'bearer\s+)[^']*(')`), `${1}[REDACTED]${2}`},
	{regexp.MustCompile(`(?i)(\b(?:password|passwd|token|secret|api[_-]?key)\b\s*[:=]\s*")(?:\\.|[^"\\])*(")`), `${1}[REDACTED]${2}`},
	{regexp.MustCompile(`(?i)(\b(?:password|passwd|token|secret|api[_-]?key)\b\s*[:=]\s*')[^']*(')`), `${1}[REDACTED]${2}`},
	{regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(\b(?:password|passwd|token|secret|api[_-]?key)\b\s*[:=]\s*)[^\s,;]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{12,}|sk-[A-Za-z0-9_-]{12,}|AIza[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16})\b`), `[REDACTED]`},
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`), `${1}[REDACTED]@`},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), `[REDACTED PRIVATE KEY]`},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*\z`), `[REDACTED PRIVATE KEY]`},
}

func boundText(value string, limit int) string {
	value, _ = redactCredentials(value)
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if len(value) > limit {
		for limit > 0 && !utf8.RuneStart(value[limit]) {
			limit--
		}
		value = value[:limit]
	}
	return value
}

func boundRequired(value string, limit int) string {
	return boundText(value, limit)
}

func optionalBound(value *string, limit int) *string {
	if value == nil {
		return nil
	}
	text := boundText(*value, limit)
	return &text
}

func optionalPath(value string) *string {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil
	}
	bounded := boundText(text, remoteviewv1.MaxPathBytes)
	if bounded == "" {
		return nil
	}
	return &bounded
}

func redactCredentials(value string) (string, bool) {
	original := value
	for _, rule := range credentialPatterns {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value, value != original
}

func uniqueBounded(values []string, limit int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		bounded := boundRequired(value, limit)
		if bounded == "" {
			continue
		}
		if _, ok := seen[bounded]; ok {
			continue
		}
		seen[bounded] = struct{}{}
		out = append(out, bounded)
	}
	sort.Strings(out)
	return out
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
