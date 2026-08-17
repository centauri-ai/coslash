// Package sessionexport maps the private collector model into the public
// session-snapshot/v1 allow-list. New fields on session.Session are excluded
// until they are explicitly mapped here.
package sessionexport

import (
	"fmt"
	"path/filepath"
	"regexp"
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
	maxIdentifierBytes   = snapshotv1.MaxIdentifierBytes
	maxStatusBytes       = 64
	maxCategoryBytes     = 64
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
	return snapshotv1.Marshal(snapshot)
}

func Build(local session.Session, options BuildOptions) (snapshotv1.Snapshot, error) {
	if local.Repository == nil || strings.TrimSpace(*local.Repository) == "" {
		return snapshotv1.Snapshot{}, fmt.Errorf("canonical repository identity is required")
	}
	b := builder{
		root:       cleanRoot(options.RepositoryRoot),
		cwd:        local.WorkingDirectory,
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
			Status:           b.optionalText("/session/status", local.Status, maxStatusBytes),
			WorkingDirectory: b.relativePath("/session/cwd", "/session", local.WorkingDirectory),
			Branch:           b.optionalText("/session/branch", local.Branch, maxBranchBytes),
			Entrypoint:       b.optionalText("/session/entrypoint", local.Entrypoint, maxEntrypointBytes),
			DurationMs:       copyIntPointer(local.DurationMs),
			LastActivityAtMs: max(local.LastActivityTime, local.StartedAt),
			LastEditAtMs:     copyInt64Pointer(local.LastEditAt),
			Model:            b.optionalRequiredText("/session/model", local.Model, maxModelBytes),
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
	cwd        string
	truncation []snapshotv1.Truncation
	redactions []snapshotv1.Redaction
	redacted   map[string]struct{}
}

var credentialPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
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
}

type boundedText struct {
	value         string
	redacted      bool
	originalBytes int
	truncated     bool
}

func boundText(value string, limit int) boundedText {
	result := boundedText{}
	if redacted, changed := redactCredentials(value); changed {
		value = redacted
		result.redacted = true
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	result.originalBytes = len(value)
	if len(value) > limit {
		for limit > 0 && !utf8.RuneStart(value[limit]) {
			limit--
		}
		value = value[:limit]
		result.truncated = true
	}
	result.value = value
	return result
}

func (b *builder) recordText(path string, value boundedText) {
	if value.redacted {
		b.redact(path, "credential_pattern")
	}
	if value.truncated {
		b.truncation = append(b.truncation, snapshotv1.Truncation{
			Path: path, Reason: snapshotv1.TruncationReasonTextBudget,
			OriginalBytes: intPointer(value.originalBytes), ExportedBytes: intPointer(len(value.value)),
		})
	}
}

func (b *builder) recordTexts(path string, values []boundedText) {
	redacted := false
	truncated := false
	originalBytes := 0
	exportedBytes := 0
	for _, value := range values {
		redacted = redacted || value.redacted
		if value.truncated && (!truncated || value.originalBytes > originalBytes) {
			truncated = true
			originalBytes = value.originalBytes
			exportedBytes = len(value.value)
		}
	}
	if redacted {
		b.redact(path, "credential_pattern")
	}
	if truncated {
		b.truncation = append(b.truncation, snapshotv1.Truncation{
			Path: path, Reason: snapshotv1.TruncationReasonTextBudget,
			OriginalBytes: intPointer(originalBytes), ExportedBytes: intPointer(exportedBytes),
		})
	}
}

func (b *builder) text(path, value string, limit int) string {
	bounded := boundText(value, limit)
	b.recordText(path, bounded)
	return bounded.value
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

func (b *builder) optionalRequiredText(path string, value *string, limit int) *string {
	if value == nil || *value == "" {
		return nil
	}
	return b.optionalText(path, value, limit)
}

func (b *builder) items(path string, original, limit int) int {
	if original <= limit {
		return original
	}
	b.truncation = append(b.truncation, snapshotv1.Truncation{
		Path: path, Reason: snapshotv1.TruncationReasonItemBudget, OriginalItems: intPointer(original), ExportedItems: intPointer(limit),
	})
	return limit
}

func (b *builder) relativePath(path, omittedPath, value string) *string {
	relative := b.repositoryRelativePath(omittedPath, value)
	if relative == nil {
		return nil
	}
	bounded := b.pathText(path, *relative)
	return &bounded
}

func (b *builder) repositoryRelativePath(omittedPath, value string) *string {
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
	return &clean
}

func (b *builder) normalizeFileEditPath(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	clean := filepath.Clean(value)
	if !filepath.IsAbs(clean) {
		cwd := strings.TrimSpace(b.cwd)
		if cwd == "" {
			cwd = b.root
		}
		if cwd == "" {
			b.redact("/session/fileEdits", "repository_root_unavailable")
			return nil
		}
		cwd = filepath.Clean(cwd)
		if !filepath.IsAbs(cwd) {
			if b.root == "" {
				b.redact("/session/fileEdits", "repository_root_unavailable")
				return nil
			}
			cwd = filepath.Join(b.root, cwd)
		}
		clean = filepath.Join(cwd, clean)
	}
	relative := b.repositoryRelativePath("/session/fileEdits", clean)
	if relative != nil && *relative == "." {
		b.redact("/session/fileEdits", "invalid_path")
		return nil
	}
	return relative
}

func (b *builder) pathText(path, value string) string {
	firstTruncation := len(b.truncation)
	value = b.text(path, value, maxPathBytes)
	for {
		separator := strings.LastIndex(value, "/")
		final := value
		if separator >= 0 {
			final = value[separator+1:]
		}
		if final != "" && final != "." && final != ".." {
			break
		}
		if separator < 0 {
			value = strings.TrimRight(value, "/")
			break
		}
		value = strings.TrimRight(value[:separator], "/")
	}
	for i := firstTruncation; i < len(b.truncation); i++ {
		if b.truncation[i].Path == path && b.truncation[i].ExportedBytes != nil {
			*b.truncation[i].ExportedBytes = len(value)
		}
	}
	return value
}

func (b *builder) redact(path, reason string) {
	key := path + "\x00" + reason
	if _, exists := b.redacted[key]; exists {
		return
	}
	if b.redacted == nil {
		b.redacted = make(map[string]struct{})
	}
	b.redacted[key] = struct{}{}
	b.redactions = append(b.redactions, snapshotv1.Redaction{Path: path, Reason: reason})
}

func (b *builder) digest(values []session.DigestEntry) []snapshotv1.Digest {
	retained := b.items("/session/digest", len(values), maxDigestItems)
	values = values[len(values)-retained:]
	result := make([]snapshotv1.Digest, 0, len(values))
	for i, value := range values {
		base := fmt.Sprintf("/session/digest/%d", i)
		item := snapshotv1.Digest{
			Turn: value.Turn, Category: b.text(base+"/category", value.Category, maxCategoryBytes),
			Description: b.text(base+"/description", value.Description, maxDigestTextBytes),
		}
		if value.Answer != "" {
			item.Answer = stringPointer(b.text(base+"/answer", value.Answer, maxDigestTextBytes))
		}
		if value.SubagentID != "" {
			item.SubagentID = stringPointer(b.text(base+"/subagentId", value.SubagentID, maxIdentifierBytes))
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
	type candidate struct {
		edit session.FileEdit
		path string
	}
	candidates := make([]candidate, 0, min(len(values), maxFileEditItems))
	validItems := 0
	for _, value := range values {
		relative := b.normalizeFileEditPath(value.Path)
		if relative == nil {
			continue
		}
		validItems++
		if len(candidates) < maxFileEditItems {
			candidates = append(candidates, candidate{edit: value, path: *relative})
		}
	}
	b.items("/session/fileEdits", validItems, maxFileEditItems)
	result := make([]snapshotv1.FileEdit, 0, len(candidates))
	for _, value := range candidates {
		path := fmt.Sprintf("/session/fileEdits/%d/path", len(result))
		result = append(result, snapshotv1.FileEdit{
			Path: b.pathText(path, value.path), Additions: value.edit.Additions, Deletions: value.edit.Deletions, Edits: value.edit.Edits, IsNew: value.edit.IsNew,
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
	usage, err := b.modelUsage(tokens, path+"/models")
	if err != nil {
		return snapshotv1.Usage{}, err
	}
	type unpricedCandidate struct {
		changes []boundedText
	}
	unpricedByName := make(map[string]*unpricedCandidate, len(unpriced))
	seenRaw := make(map[string]struct{}, len(unpriced))
	for _, model := range unpriced {
		if _, exists := seenRaw[model]; exists {
			continue
		}
		seenRaw[model] = struct{}{}
		if strings.TrimSpace(model) == "" {
			b.redact(path+"/unpricedModels", "invalid_model")
			continue
		}
		bounded := boundText(model, maxModelBytes)
		candidate := unpricedByName[bounded.value]
		if candidate == nil {
			candidate = &unpricedCandidate{}
			unpricedByName[bounded.value] = candidate
		}
		candidate.changes = append(candidate.changes, bounded)
	}
	names := make([]string, 0, len(unpricedByName))
	for name := range unpricedByName {
		names = append(names, name)
	}
	sort.Strings(names)
	names = names[:b.items(path+"/unpricedModels", len(names), maxUnpricedModels)]
	boundedUnpriced := make([]string, 0, len(names))
	for i, name := range names {
		modelPath := fmt.Sprintf("%s/unpricedModels/%d", path, i)
		b.recordTexts(modelPath, unpricedByName[name].changes)
		boundedUnpriced = append(boundedUnpriced, name)
	}
	return snapshotv1.Usage{Models: usage, EstimatedCostMicroUSD: estimated, UnpricedModels: boundedUnpriced}, nil
}

func (b *builder) modelUsage(tokens map[string]session.ModelTokens, path string) ([]snapshotv1.ModelUsage, error) {
	type usageCandidate struct {
		usage   snapshotv1.ModelUsage
		changes []boundedText
	}
	usageByName := make(map[string]*usageCandidate, len(tokens))
	for model, value := range tokens {
		if strings.TrimSpace(model) == "" {
			b.redact(path, "invalid_model")
			continue
		}
		bounded := boundText(model, maxModelBytes)
		modelCost, err := snapshotv1.CostMicroUSD(value.Cost)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		boundedUsage := snapshotv1.ModelUsage{
			Model:       bounded.value,
			InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
			CacheCreationInputTokens:   value.CacheCreationInputTokens,
			CacheCreation1hInputTokens: value.CacheCreation1hInputTokens,
			CacheReadInputTokens:       value.CacheReadInputTokens, EstimatedCostMicroUSD: modelCost,
		}
		candidate := usageByName[bounded.value]
		if candidate == nil {
			candidate = &usageCandidate{usage: boundedUsage}
			usageByName[bounded.value] = candidate
		} else {
			mergeModelUsage(&candidate.usage, boundedUsage)
		}
		candidate.changes = append(candidate.changes, bounded)
	}
	names := make([]string, 0, len(usageByName))
	for name := range usageByName {
		names = append(names, name)
	}
	sort.Strings(names)
	names = names[:b.items(path, len(names), snapshotv1.MaxUsageModels)]
	usage := make([]snapshotv1.ModelUsage, 0, len(names))
	for i, name := range names {
		modelPath := fmt.Sprintf("%s/%d/model", path, i)
		candidate := usageByName[name]
		b.recordTexts(modelPath, candidate.changes)
		usage = append(usage, candidate.usage)
	}
	return usage, nil
}

func mergeModelUsage(target *snapshotv1.ModelUsage, value snapshotv1.ModelUsage) {
	target.InputTokens += value.InputTokens
	target.OutputTokens += value.OutputTokens
	target.CacheCreationInputTokens += value.CacheCreationInputTokens
	target.CacheCreation1hInputTokens += value.CacheCreation1hInputTokens
	target.CacheReadInputTokens += value.CacheReadInputTokens
	target.EstimatedCostMicroUSD += value.EstimatedCostMicroUSD
}

func (b *builder) subagents(values []session.Subagent) ([]snapshotv1.Subagent, error) {
	values = values[:b.items("/session/subagents", len(values), maxSubagentItems)]
	result := make([]snapshotv1.Subagent, 0, len(values))
	commandLabels := 0
	for i, value := range values {
		base := fmt.Sprintf("/session/subagents/%d", i)
		usage, err := b.modelUsage(value.Tokens, base+"/usage")
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
			ID: b.text(base+"/id", value.ID, maxIdentifierBytes), Name: b.text(base+"/name", value.Name, maxNameBytes),
			Model: b.optionalRequiredText(base+"/model", value.Model, maxModelBytes), Status: b.text(base+"/status", value.Status, maxStatusBytes),
			Task: b.text(base+"/task", value.Task, maxSubagentTextBytes), Result: b.text(base+"/result", value.Result, maxSubagentTextBytes),
			DurationMs: copyIntPointer(value.DurationMs), SpawnedAtTurn: copyIntPointer(value.SpawnedAtTurn), ToolUses: value.ToolUses,
			CommandLabels: labels, Usage: usage, EstimatedCostMicroUSD: cost,
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
