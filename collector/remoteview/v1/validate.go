package remoteviewv1

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

func ValidateView(v View) error {
	if v.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion must be %q", SchemaVersion)
	}
	if err := boundedRequired("collectorVersion", v.CollectorVersion, MaxCollectorVersionBytes); err != nil {
		return err
	}
	if err := validateCapabilities(v.Capabilities); err != nil {
		return err
	}
	if !slices.Contains(v.Capabilities, CapabilityRemoteView) {
		return fmt.Errorf("capabilities must include %q", CapabilityRemoteView)
	}
	if err := validateLaunchableAgents(v.LaunchableAgents); err != nil {
		return err
	}
	if v.RequestedSinceMs < 0 || v.RequestNowMs < 0 || v.HostNowMs < 0 || v.CollectedAtMs < 0 || v.CoverageSinceMs < 0 {
		return fmt.Errorf("clock fields must be non-negative")
	}
	if v.RequestedSinceMs > v.RequestNowMs {
		return fmt.Errorf("requestedSinceMs must not exceed requestNowMs")
	}
	if v.CoverageSinceMs != v.RequestedSinceMs {
		return fmt.Errorf("coverageSinceMs must equal requestedSinceMs")
	}
	if err := validateTruncation(v.Truncated, v.TruncationReason); err != nil {
		return err
	}
	if err := validateHost(v.Host); err != nil {
		return err
	}
	if v.Sessions == nil {
		return fmt.Errorf("sessions must be an array")
	}
	if len(v.Sessions) > MaxSessions {
		return fmt.Errorf("sessions exceed limit")
	}
	for i, session := range v.Sessions {
		if err := validateSession(session, i); err != nil {
			return err
		}
	}
	return nil
}

func ValidateProbe(p Probe) error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion must be %q", SchemaVersion)
	}
	if err := boundedRequired("collectorVersion", p.CollectorVersion, MaxCollectorVersionBytes); err != nil {
		return err
	}
	if err := validateCapabilities(p.Capabilities); err != nil {
		return err
	}
	if !slices.Contains(p.Capabilities, CapabilityRemoteView) {
		return fmt.Errorf("capabilities must include %q", CapabilityRemoteView)
	}
	if err := validateLaunchableAgents(p.LaunchableAgents); err != nil {
		return err
	}
	if p.HostNowMs < 0 {
		return fmt.Errorf("hostNowMs must be non-negative")
	}
	return validateHost(p.Host)
}

func validateTruncation(truncated bool, reason *string) error {
	if !truncated {
		if reason != nil {
			return fmt.Errorf("truncationReason must be omitted when truncated is false")
		}
		return nil
	}
	if reason == nil {
		return fmt.Errorf("truncationReason is required when truncated is true")
	}
	if *reason != TruncationReasonSession && *reason != TruncationReasonPayload {
		return fmt.Errorf("unknown truncationReason")
	}
	return nil
}

func validateHost(host Host) error {
	if err := boundedRequired("host.os", host.OS, 64); err != nil {
		return err
	}
	return boundedRequired("host.arch", host.Arch, 64)
}

func validateCapabilities(values []string) error {
	if values == nil {
		return fmt.Errorf("capabilities must be an array")
	}
	if len(values) == 0 || len(values) > MaxCapabilities {
		return fmt.Errorf("capabilities exceed limit")
	}
	seen := map[string]struct{}{}
	for i, value := range values {
		if err := boundedRequired(fmt.Sprintf("capabilities[%d]", i), value, MaxCapabilityBytes); err != nil {
			return err
		}
		if !validCapability(value) {
			return fmt.Errorf("capabilities[%d] must be a versioned identifier", i)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("capabilities must be unique")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateLaunchableAgents(values []string) error {
	if values == nil {
		return fmt.Errorf("launchableAgents must be an array")
	}
	if len(values) > MaxLaunchableAgents {
		return fmt.Errorf("launchableAgents exceed limit")
	}
	seen := map[string]struct{}{}
	for i, value := range values {
		if !validRemoteAgent(value) {
			return fmt.Errorf("launchableAgents[%d] must be a supported agent", i)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("launchableAgents must be unique")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateSession(s Session, index int) error {
	prefix := fmt.Sprintf("sessions[%d]", index)
	if !validRemoteAgent(s.Agent) {
		return fmt.Errorf("%s.agent must be a supported agent", prefix)
	}
	if err := boundedRequired(prefix+".sourceSessionId", s.SourceSessionID, MaxIdentifierBytes); err != nil {
		return err
	}
	fields := []struct {
		name  string
		value *string
		limit int
	}{
		{"name", s.Name, MaxNameBytes},
		{"summary", s.Summary, MaxSummaryBytes},
		{"status", s.Status, 64},
		{"cwd", s.WorkingDirectory, MaxPathBytes},
		{"repository", s.Repository, MaxRepositoryBytes},
		{"branch", s.Branch, MaxBranchBytes},
		{"entrypoint", s.Entrypoint, MaxEntrypointBytes},
		{"declaredGoal", s.DeclaredGoal, MaxGoalBytes},
		{"firstPrompt", s.FirstPrompt, MaxPromptBytes},
	}
	for _, field := range fields {
		if field.value != nil {
			if err := bounded(prefix+"."+field.name, *field.value, field.limit); err != nil {
				return err
			}
		}
	}
	if s.Model != nil {
		if err := boundedRequired(prefix+".model", *s.Model, MaxModelBytes); err != nil {
			return err
		}
	}
	if s.SessionStartedAtMs <= 0 {
		return fmt.Errorf("%s.sessionStartedAtMs must be positive", prefix)
	}
	if s.LastActivityAtMs < 0 {
		return fmt.Errorf("%s.lastActivityAtMs must be non-negative", prefix)
	}
	if s.SessionStartedAtMs > s.LastActivityAtMs {
		return fmt.Errorf("%s.sessionStartedAtMs must not exceed lastActivityAtMs", prefix)
	}
	if s.LastEditAtMs != nil && *s.LastEditAtMs < 0 {
		return fmt.Errorf("%s.lastEditAtMs must be non-negative", prefix)
	}
	if s.DurationMs != nil && *s.DurationMs < 0 {
		return fmt.Errorf("%s.durationMs must be non-negative", prefix)
	}
	if s.ContextTokens != nil && *s.ContextTokens < 0 {
		return fmt.Errorf("%s.contextTokens must be non-negative", prefix)
	}
	if s.ContextWindow != nil && *s.ContextWindow < 0 {
		return fmt.Errorf("%s.contextWindow must be non-negative", prefix)
	}
	counts := []struct {
		name  string
		value int
	}{
		{"editedFiles", s.Counts.EditedFiles},
		{"turns", s.Counts.Turns},
		{"toolUses", s.Counts.ToolUses},
		{"errors", s.Counts.Errors},
		{"compactions", s.Counts.Compactions},
		{"commands", s.Counts.Commands},
		{"pullRequests", s.Counts.PullRequests},
	}
	for _, count := range counts {
		if count.value < 0 {
			return fmt.Errorf("%s.counts.%s must be non-negative", prefix, count.name)
		}
	}
	if s.Usage.EstimatedCostMicroUSD < 0 {
		return fmt.Errorf("%s.usage cost must be non-negative", prefix)
	}
	if s.Usage.Models == nil || s.Usage.UnpricedModels == nil || s.Digest == nil || s.Todos == nil || s.FileEdits == nil || s.Commits == nil || s.Subagents == nil {
		return fmt.Errorf("%s collection fields must be arrays, not null", prefix)
	}
	if len(s.Usage.Models) > MaxUsageModels || len(s.Usage.UnpricedModels) > MaxUnpricedModels ||
		len(s.Digest) > MaxDigestItems || len(s.Todos) > MaxTodoItems || len(s.FileEdits) > MaxFileEditItems ||
		len(s.Commits) > MaxCommitItems || len(s.Subagents) > MaxSubagentItems {
		return fmt.Errorf("%s collection exceeds item budget", prefix)
	}
	if !sortedUniqueUsage(s.Usage.Models) || !sortedUniqueStrings(s.Usage.UnpricedModels) {
		return fmt.Errorf("%s usage model lists must be sorted and unique", prefix)
	}
	for _, usage := range s.Usage.Models {
		if err := validateUsage(usage); err != nil {
			return err
		}
	}
	for i, model := range s.Usage.UnpricedModels {
		if err := boundedRequired(fmt.Sprintf("%s.usage.unpricedModels[%d]", prefix, i), model, MaxModelBytes); err != nil {
			return err
		}
	}
	for i, digest := range s.Digest {
		if digest.Turn < 0 {
			return fmt.Errorf("%s.digest[%d].turn must be non-negative", prefix, i)
		}
		if err := boundedRequired(fmt.Sprintf("%s.digest[%d].category", prefix, i), digest.Category, 64); err != nil {
			return err
		}
		if err := bounded(fmt.Sprintf("%s.digest[%d].description", prefix, i), digest.Description, MaxDigestTextBytes); err != nil {
			return err
		}
		if digest.Answer != nil {
			if err := bounded(fmt.Sprintf("%s.digest[%d].answer", prefix, i), *digest.Answer, MaxDigestTextBytes); err != nil {
				return err
			}
		}
		if digest.SubagentID != nil {
			if err := boundedRequired(fmt.Sprintf("%s.digest[%d].subagentId", prefix, i), *digest.SubagentID, MaxIdentifierBytes); err != nil {
				return err
			}
		}
	}
	for i, todo := range s.Todos {
		if err := bounded(fmt.Sprintf("%s.todos[%d].text", prefix, i), todo.Text, MaxTodoTextBytes); err != nil {
			return err
		}
	}
	for i, edit := range s.FileEdits {
		if err := boundedRequired(fmt.Sprintf("%s.fileEdits[%d].path", prefix, i), edit.Path, MaxPathBytes); err != nil {
			return err
		}
		if edit.Additions < 0 || edit.Deletions < 0 || edit.Edits < 0 {
			return fmt.Errorf("%s.fileEdits[%d] counters must be non-negative", prefix, i)
		}
	}
	for i, commit := range s.Commits {
		if err := bounded(fmt.Sprintf("%s.commits[%d]", prefix, i), commit, MaxCommitTextBytes); err != nil {
			return err
		}
	}
	if s.Git != nil {
		if err := boundedRequired(prefix+".git.baseBranch", s.Git.BaseBranch, MaxBranchBytes); err != nil {
			return err
		}
		if s.Git.Ahead < 0 || s.Git.Behind < 0 {
			return fmt.Errorf("%s.git drift counts must be non-negative", prefix)
		}
	}
	commandLabels := 0
	for i, subagent := range s.Subagents {
		if subagent.ToolUses < 0 || subagent.EstimatedCostMicroUSD < 0 || subagent.CommandLabels == nil || subagent.Usage == nil {
			return fmt.Errorf("%s.subagents[%d] is invalid", prefix, i)
		}
		if err := boundedRequired(fmt.Sprintf("%s.subagents[%d].id", prefix, i), subagent.ID, MaxIdentifierBytes); err != nil {
			return err
		}
		if err := boundedRequired(fmt.Sprintf("%s.subagents[%d].name", prefix, i), subagent.Name, MaxNameBytes); err != nil {
			return err
		}
		if subagent.Model != nil {
			if err := boundedRequired(fmt.Sprintf("%s.subagents[%d].model", prefix, i), *subagent.Model, MaxModelBytes); err != nil {
				return err
			}
		}
		if err := boundedRequired(fmt.Sprintf("%s.subagents[%d].status", prefix, i), subagent.Status, 64); err != nil {
			return err
		}
		if err := bounded(fmt.Sprintf("%s.subagents[%d].task", prefix, i), subagent.Task, MaxSubagentTextBytes); err != nil {
			return err
		}
		if err := bounded(fmt.Sprintf("%s.subagents[%d].result", prefix, i), subagent.Result, MaxSubagentTextBytes); err != nil {
			return err
		}
		if subagent.DurationMs != nil && *subagent.DurationMs < 0 {
			return fmt.Errorf("%s.subagents[%d].durationMs must be non-negative", prefix, i)
		}
		if subagent.SpawnedAtTurn != nil && *subagent.SpawnedAtTurn < 0 {
			return fmt.Errorf("%s.subagents[%d].spawnedAtTurn must be non-negative", prefix, i)
		}
		commandLabels += len(subagent.CommandLabels)
		for j, label := range subagent.CommandLabels {
			if err := boundedRequired(fmt.Sprintf("%s.subagents[%d].commandLabels[%d]", prefix, i, j), label, MaxCommandLabelBytes); err != nil {
				return err
			}
		}
		if len(subagent.Usage) > MaxUsageModels || !sortedUniqueUsage(subagent.Usage) {
			return fmt.Errorf("%s.subagents[%d].usage must be sorted and unique", prefix, i)
		}
		for _, usage := range subagent.Usage {
			if err := validateUsage(usage); err != nil {
				return err
			}
		}
	}
	if commandLabels > MaxCommandLabelItems {
		return fmt.Errorf("%s subagent command labels exceed item budget", prefix)
	}
	return nil
}

func validateUsage(u ModelUsage) error {
	if boundedRequired("usage.model", u.Model, MaxModelBytes) != nil ||
		u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheCreationInputTokens < 0 ||
		u.CacheCreation1hInputTokens < 0 || u.CacheReadInputTokens < 0 || u.EstimatedCostMicroUSD < 0 {
		return fmt.Errorf("model usage fields must be non-negative and model is required")
	}
	return nil
}

func boundedRequired(name, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return bounded(name, value, maxBytes)
}

func bounded(name, value string, maxBytes int) error {
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return fmt.Errorf("%s exceeds its UTF-8 byte budget", name)
	}
	return nil
}

func validRemoteAgent(value string) bool {
	return value == AgentClaude || value == AgentCodex
}

func validCapability(value string) bool {
	if value == "" || len(value) > MaxCapabilityBytes || strings.Contains(value, " ") {
		return false
	}
	slash := strings.LastIndexByte(value, '/')
	if slash <= 0 || slash == len(value)-1 {
		return false
	}
	name, version := value[:slash], value[slash+1:]
	if !validCapabilityName(name) || !validCapabilityVersion(version) {
		return false
	}
	return true
}

func validCapabilityName(value string) bool {
	for i := range len(value) {
		char := value[i]
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
		if i == 0 && (char < 'a' || char > 'z') {
			return false
		}
	}
	return value != ""
}

func validCapabilityVersion(value string) bool {
	if len(value) < 2 || value[0] != 'v' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func sortedUniqueStrings(values []string) bool {
	return slices.IsSorted(values) && len(slices.Compact(slices.Clone(values))) == len(values)
}

func sortedUniqueUsage(values []ModelUsage) bool {
	for i := range values {
		if i > 0 && values[i-1].Model >= values[i].Model {
			return false
		}
	}
	return true
}

// CostMicroUSD freezes a floating-point local estimate at the wire boundary.
func CostMicroUSD(dollars float64) (int64, error) {
	if math.IsNaN(dollars) || math.IsInf(dollars, 0) || dollars < 0 || dollars >= float64(math.MaxInt64)/1_000_000 {
		return 0, fmt.Errorf("estimated cost is not a finite non-negative value")
	}
	return int64(math.Round(dollars * 1_000_000)), nil
}
