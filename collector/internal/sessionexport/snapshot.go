// Package sessionexport maps the private collector model into the public
// session-snapshot/v1 allow-list. New fields on session.Session are excluded
// until they are explicitly mapped here.
package sessionexport

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

const (
	maxNameBytes         = snapshotv1.MaxNameBytes
	maxSummaryBytes      = snapshotv1.MaxSummaryBytes
	maxPathBytes         = snapshotv1.MaxPathBytes
	maxBranchBytes       = snapshotv1.MaxBranchBytes
	maxEntrypointBytes   = snapshotv1.MaxEntrypointBytes
	maxModelBytes        = snapshotv1.MaxModelBytes
	maxGoalBytes         = snapshotv1.MaxGoalBytes
	maxPromptBytes       = snapshotv1.MaxPromptBytes
	maxDigestItems       = snapshotv1.MaxDigestItems
	maxDigestTextBytes   = snapshotv1.MaxDigestTextBytes
	maxTodoItems         = snapshotv1.MaxTodoItems
	maxTodoTextBytes     = snapshotv1.MaxTodoTextBytes
	maxFileEditItems     = snapshotv1.MaxFileEditItems
	maxCommitItems       = snapshotv1.MaxCommitItems
	maxCommitTextBytes   = snapshotv1.MaxCommitTextBytes
	maxSubagentItems     = snapshotv1.MaxSubagentItems
	maxSubagentTextBytes = snapshotv1.MaxSubagentTextBytes
	maxCommandLabelItems = snapshotv1.MaxCommandLabelItems
	maxCommandLabelBytes = snapshotv1.MaxCommandLabelBytes
	maxUnpricedModels    = snapshotv1.MaxUnpricedModels
)

// BuildOptions supplies facts intentionally kept outside the local Session
// model. RepositoryRoot is needed to prove exported paths are relative.
type BuildOptions struct {
	CollectorVersion string
	RepositoryRoot   string
}

// Marshal builds and serializes the exact bytes used by preview and upload.
func Marshal(local session.Session, options BuildOptions) ([]byte, error) {
	snapshot, err := Build(local, options)
	if err != nil {
		return nil, err
	}
	snapshot, err = fitAggregate(snapshot)
	if err != nil {
		return nil, err
	}
	return snapshotv1.Marshal(snapshot)
}

func Build(local session.Session, options BuildOptions) (snapshotv1.Snapshot, error) {
	if local.Repository == nil || strings.TrimSpace(*local.Repository) == "" {
		return snapshotv1.Snapshot{}, fmt.Errorf("canonical repository identity is required")
	}
	b := builder{
		root:       cleanRoot(options.RepositoryRoot),
		truncation: []snapshotv1.Truncation{},
		redactions: []snapshotv1.Redaction{},
	}
	usage, err := b.usage(local.Tokens, local.Cost, local.UnpricedModels, "/session/usage")
	if err != nil {
		return snapshotv1.Snapshot{}, err
	}
	s := snapshotv1.Snapshot{
		SchemaVersion:      snapshotv1.SchemaVersion,
		MediaType:          snapshotv1.MediaType,
		CollectorVersion:   options.CollectorVersion,
		Agent:              local.Agent,
		SourceSessionID:    local.ID,
		SessionStartedAtMs: local.StartedAt,
		Repository: snapshotv1.Repository{
			Canonical: b.text("/repository/canonical", *local.Repository, snapshotv1.MaxRepositoryBytes),
			LocalOnly: local.RepositoryLocalOnly,
		},
		Truncation: []snapshotv1.Truncation{},
		Redactions: []snapshotv1.Redaction{},
		Session: snapshotv1.Session{
			Name:             b.optionalText("/session/name", local.Name, maxNameBytes),
			Summary:          b.optionalText("/session/summary", local.Summary, maxSummaryBytes),
			Status:           b.optionalText("/session/status", local.Status, 64),
			WorkingDirectory: b.relativePath("/session/cwd", "/session", local.WorkingDirectory),
			Branch:           b.optionalText("/session/branch", local.Branch, maxBranchBytes),
			Entrypoint:       b.optionalText("/session/entrypoint", local.Entrypoint, maxEntrypointBytes),
			DurationMs:       copyIntPointer(local.DurationMs),
			LastActivityAtMs: local.LastActivityTime,
			LastEditAtMs:     copyInt64Pointer(local.LastEditAt),
			Model:            b.optionalText("/session/model", local.Model, maxModelBytes),
			ContextTokens:    copyIntPointer(local.ContextTokens),
			ContextWindow:    copyIntPointer(local.ContextWindow),
			DeclaredGoal:     b.optionalText("/session/declaredGoal", local.DeclaredGoal, maxGoalBytes),
			FirstPrompt:      b.optionalText("/session/firstPrompt", local.FirstPrompt, maxPromptBytes),
			Counts: snapshotv1.Counts{
				EditedFiles:  local.EditedFileCount,
				Turns:        local.Turns,
				ToolUses:     local.ToolUses,
				Errors:       local.Errors,
				Compactions:  local.Compactions,
				Commands:     len(local.Commands),
				PullRequests: local.PullRequests,
			},
			Usage:     usage,
			Digest:    b.digest(local.Digest),
			Todos:     b.todos(local.Todos),
			FileEdits: b.fileEdits(local.FileEdits),
			Commits:   b.strings("/session/commits", local.Commits, maxCommitItems, maxCommitTextBytes),
			Git:       b.git(local.Git),
			Subagents: []snapshotv1.Subagent{},
		},
	}
	s.Session.Subagents, err = b.subagents(local.Subagents)
	if err != nil {
		return snapshotv1.Snapshot{}, err
	}
	s.Truncation = b.truncation
	s.Redactions = b.redactions
	if err := snapshotv1.Validate(s, false); err != nil {
		return snapshotv1.Snapshot{}, fmt.Errorf("local session cannot produce a valid snapshot: %w", err)
	}
	return s, nil
}

type builder struct {
	root       string
	truncation []snapshotv1.Truncation
	redactions []snapshotv1.Redaction
}

var credentialPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^\s"']+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(\b(?:password|passwd|token|secret|api[_-]?key)\b\s*[:=]\s*)[^\s,;]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{12,}|sk-[A-Za-z0-9_-]{12,}|AIza[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16})\b`), `[REDACTED]`},
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`), `${1}[REDACTED]@`},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), `[REDACTED PRIVATE KEY]`},
}

func (b *builder) text(path, value string, limit int) string {
	if redacted, changed := redactCredentials(value); changed {
		value = redacted
		b.redact(path, "credential_pattern")
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if len(value) <= limit {
		return value
	}
	original := len(value)
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	value = value[:limit]
	b.truncation = append(b.truncation, snapshotv1.Truncation{
		Path: path, Reason: "text_budget", OriginalBytes: intPointer(original), ExportedBytes: intPointer(len(value)),
	})
	return value
}

func redactCredentials(value string) (string, bool) {
	original := value
	for _, rule := range credentialPatterns {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value, value != original
}

func (b *builder) optionalText(path string, value *string, limit int) *string {
	if value == nil {
		return nil
	}
	text := b.text(path, *value, limit)
	return &text
}

func (b *builder) items(path string, original, limit int) int {
	if original <= limit {
		return original
	}
	b.truncation = append(b.truncation, snapshotv1.Truncation{
		Path: path, Reason: "item_budget", OriginalItems: intPointer(original), ExportedItems: intPointer(limit),
	})
	return limit
}

func (b *builder) relativePath(path, omittedPath, value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	clean := filepath.Clean(value)
	if filepath.IsAbs(clean) {
		if b.root == "" {
			b.redact(omittedPath, "repository_root_unavailable")
			return nil
		}
		rel, err := filepath.Rel(b.root, clean)
		if err != nil || outsideRoot(rel) {
			b.redact(omittedPath, "outside_repository")
			return nil
		}
		clean = rel
	}
	if outsideRoot(clean) {
		b.redact(omittedPath, "outside_repository")
		return nil
	}
	clean = filepath.ToSlash(clean)
	clean = b.text(path, clean, maxPathBytes)
	return &clean
}

func (b *builder) redact(path, reason string) {
	b.redactions = append(b.redactions, snapshotv1.Redaction{Path: path, Reason: reason})
}

func (b *builder) digest(values []session.DigestEntry) []snapshotv1.Digest {
	values = values[:b.items("/session/digest", len(values), maxDigestItems)]
	result := make([]snapshotv1.Digest, 0, len(values))
	for i, value := range values {
		base := fmt.Sprintf("/session/digest/%d", i)
		item := snapshotv1.Digest{
			Turn: value.Turn, Category: b.text(base+"/category", value.Category, 64),
			Description: b.text(base+"/description", value.Description, maxDigestTextBytes),
		}
		if value.Answer != "" {
			item.Answer = stringPointer(b.text(base+"/answer", value.Answer, maxDigestTextBytes))
		}
		if value.SubagentID != "" {
			item.SubagentID = stringPointer(b.text(base+"/subagentId", value.SubagentID, 512))
		}
		result = append(result, item)
	}
	return result
}

func (b *builder) todos(values []session.Todo) []snapshotv1.Todo {
	values = values[:b.items("/session/todos", len(values), maxTodoItems)]
	result := make([]snapshotv1.Todo, 0, len(values))
	for i, value := range values {
		result = append(result, snapshotv1.Todo{Text: b.text(fmt.Sprintf("/session/todos/%d/text", i), value.Text, maxTodoTextBytes), Done: value.Done})
	}
	return result
}

func (b *builder) fileEdits(values []session.FileEdit) []snapshotv1.FileEdit {
	values = values[:b.items("/session/fileEdits", len(values), maxFileEditItems)]
	result := make([]snapshotv1.FileEdit, 0, len(values))
	for _, value := range values {
		path := fmt.Sprintf("/session/fileEdits/%d/path", len(result))
		relative := b.relativePath(path, "/session/fileEdits", value.Path)
		if relative == nil {
			continue
		}
		result = append(result, snapshotv1.FileEdit{
			Path: *relative, Additions: value.Additions, Deletions: value.Deletions, Edits: value.Edits, IsNew: value.IsNew,
		})
	}
	return result
}

func (b *builder) strings(path string, values []string, maxItems, maxBytes int) []string {
	values = values[:b.items(path, len(values), maxItems)]
	result := make([]string, 0, len(values))
	for i, value := range values {
		result = append(result, b.text(fmt.Sprintf("%s/%d", path, i), value, maxBytes))
	}
	return result
}

func (b *builder) git(value *session.GitDrift) *snapshotv1.GitDrift {
	if value == nil {
		return nil
	}
	return &snapshotv1.GitDrift{BaseBranch: b.text("/session/git/baseBranch", value.BaseBranch, maxBranchBytes), Ahead: value.Ahead, Behind: value.Behind}
}

func (b *builder) usage(tokens map[string]session.ModelTokens, cost float64, unpriced []string, path string) (snapshotv1.Usage, error) {
	estimated, err := snapshotv1.CostMicroUSD(cost)
	if err != nil {
		return snapshotv1.Usage{}, fmt.Errorf("%s: %w", path, err)
	}
	models := make([]string, 0, len(tokens))
	for model := range tokens {
		models = append(models, model)
	}
	sort.Strings(models)
	models = models[:b.items(path+"/models", len(models), snapshotv1.MaxUsageModels)]
	usage := make([]snapshotv1.ModelUsage, 0, len(models))
	for i, model := range models {
		value := tokens[model]
		modelCost, err := snapshotv1.CostMicroUSD(value.Cost)
		if err != nil {
			return snapshotv1.Usage{}, fmt.Errorf("%s/models/%d: %w", path, i, err)
		}
		usage = append(usage, snapshotv1.ModelUsage{
			Model:       b.text(fmt.Sprintf("%s/models/%d/model", path, i), model, maxModelBytes),
			InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
			CacheCreationInputTokens:   value.CacheCreationInputTokens,
			CacheCreation1hInputTokens: value.CacheCreation1hInputTokens,
			CacheReadInputTokens:       value.CacheReadInputTokens, EstimatedCostMicroUSD: modelCost,
		})
	}
	unpriced = slices.Clone(unpriced)
	sort.Strings(unpriced)
	unpriced = slices.Compact(unpriced)
	unpriced = unpriced[:b.items(path+"/unpricedModels", len(unpriced), maxUnpricedModels)]
	for i := range unpriced {
		unpriced[i] = b.text(fmt.Sprintf("%s/unpricedModels/%d", path, i), unpriced[i], maxModelBytes)
	}
	if unpriced == nil {
		unpriced = []string{}
	}
	return snapshotv1.Usage{Models: usage, EstimatedCostMicroUSD: estimated, UnpricedModels: unpriced}, nil
}

func (b *builder) subagents(values []session.Subagent) ([]snapshotv1.Subagent, error) {
	values = values[:b.items("/session/subagents", len(values), maxSubagentItems)]
	result := make([]snapshotv1.Subagent, 0, len(values))
	commandLabels := 0
	for i, value := range values {
		base := fmt.Sprintf("/session/subagents/%d", i)
		usage, err := b.usage(value.Tokens, value.Cost, nil, base+"/usage")
		if err != nil {
			return nil, err
		}
		labels := make([]string, 0, len(value.Commands))
		candidates := 0
		rawOmitted := false
		for _, command := range value.Commands {
			if strings.TrimSpace(command.Label) == "" || command.Label == command.Command {
				rawOmitted = true
				continue
			}
			candidates++
			if commandLabels >= maxCommandLabelItems {
				continue
			}
			labels = append(labels, b.text(fmt.Sprintf("%s/commandLabels/%d", base, len(labels)), command.Label, maxCommandLabelBytes))
			commandLabels++
		}
		if rawOmitted {
			b.redact(base+"/commandLabels", "raw_command")
		}
		if candidates > len(labels) {
			b.truncation = append(b.truncation, snapshotv1.Truncation{
				Path: base + "/commandLabels", Reason: snapshotv1.TruncationReasonItemBudget,
				OriginalItems: intPointer(candidates), ExportedItems: intPointer(len(labels)),
			})
		}
		cost, err := snapshotv1.CostMicroUSD(value.Cost)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshotv1.Subagent{
			ID: b.text(base+"/id", value.ID, 512), Name: b.text(base+"/name", value.Name, maxNameBytes),
			Model: b.optionalText(base+"/model", value.Model, maxModelBytes), Status: b.text(base+"/status", value.Status, 64),
			Task: b.text(base+"/task", value.Task, maxSubagentTextBytes), Result: b.text(base+"/result", value.Result, maxSubagentTextBytes),
			DurationMs: copyIntPointer(value.DurationMs), SpawnedAtTurn: copyIntPointer(value.SpawnedAtTurn), ToolUses: value.ToolUses,
			CommandLabels: labels, Usage: usage.Models, EstimatedCostMicroUSD: cost,
		})
	}
	return result, nil
}

func cleanRoot(root string) string {
	if root == "" {
		return ""
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	return filepath.Clean(root)
}

func outsideRoot(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || filepath.IsAbs(path)
}

func copyIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func intPointer(value int) *int          { return &value }
func stringPointer(value string) *string { return &value }
