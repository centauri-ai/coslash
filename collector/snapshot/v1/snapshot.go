// Package snapshotv1 defines the portable session-snapshot/v1 wire contract.
//
// Keep this package independent from collector internals. Publishers map their
// local model into these types; consumers may validate fixtures without
// importing parser or UI implementation details.
package snapshotv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion            = "session-snapshot/v1"
	MediaType                = "application/vnd.coslash.session-snapshot.v1+json"
	MaxPayloadBytes          = 256 * 1024
	MaxCollectorVersionBytes = 64
	MaxAgentBytes            = 64
	MaxIdentifierBytes       = 512
	MaxRepositoryBytes       = 1024
	MaxNameBytes             = 512
	MaxSummaryBytes          = 4 * 1024
	MaxPathBytes             = 1024
	MaxBranchBytes           = 512
	MaxEntrypointBytes       = 512
	MaxModelBytes            = 256
	MaxGoalBytes             = 16 * 1024
	MaxPromptBytes           = 16 * 1024
	MaxDigestItems           = 200
	MaxDigestTextBytes       = 4 * 1024
	MaxTodoItems             = 200
	MaxTodoTextBytes         = 2 * 1024
	MaxFileEditItems         = 2000
	MaxCommitItems           = 200
	MaxCommitTextBytes       = 2 * 1024
	// Commit SHAs are deterministic Git object locators, intentionally
	// distinct from the human-facing commit subjects in Commits.
	MaxCommitSHAItems               = 200
	MaxSubagentItems                = 100
	MaxSubagentTextBytes            = 8 * 1024
	MaxCommandLabelItems            = 200
	MaxCommandLabelBytes            = 512
	MaxUnpricedModels               = 100
	MaxUsageModels                  = 100
	TruncationReasonTextBudget      = "text_budget"
	TruncationReasonItemBudget      = "item_budget"
	TruncationReasonAggregateBudget = "aggregate_budget"
)

type Snapshot struct {
	SchemaVersion      string       `json:"schemaVersion"`
	MediaType          string       `json:"mediaType"`
	CollectorVersion   string       `json:"collectorVersion"`
	Agent              string       `json:"agent"`
	SourceSessionID    string       `json:"sourceSessionId"`
	SessionStartedAtMs int64        `json:"sessionStartedAtMs"`
	ContentHash        string       `json:"contentHash,omitempty"`
	Repository         Repository   `json:"repository"`
	Truncation         []Truncation `json:"truncation"`
	Redactions         []Redaction  `json:"redactions"`
	Session            Session      `json:"session"`
}

type Repository struct {
	Canonical string `json:"canonical"`
	LocalOnly bool   `json:"localOnly"`
}

type Truncation struct {
	Path          string `json:"path"`
	Reason        string `json:"reason"`
	OriginalBytes *int   `json:"originalBytes,omitempty"`
	ExportedBytes *int   `json:"exportedBytes,omitempty"`
	OriginalItems *int   `json:"originalItems,omitempty"`
	ExportedItems *int   `json:"exportedItems,omitempty"`
}

type Redaction struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Session struct {
	Name             *string    `json:"name,omitempty"`
	Summary          *string    `json:"summary,omitempty"`
	Status           *string    `json:"status,omitempty"`
	WorkingDirectory *string    `json:"cwd,omitempty"`
	Branch           *string    `json:"branch,omitempty"`
	Entrypoint       *string    `json:"entrypoint,omitempty"`
	DurationMs       *int       `json:"durationMs,omitempty"`
	LastActivityAtMs int64      `json:"lastActivityAtMs"`
	LastEditAtMs     *int64     `json:"lastEditAtMs,omitempty"`
	Model            *string    `json:"model,omitempty"`
	ContextTokens    *int       `json:"contextTokens,omitempty"`
	ContextWindow    *int       `json:"contextWindow,omitempty"`
	DeclaredGoal     *string    `json:"declaredGoal,omitempty"`
	FirstPrompt      *string    `json:"firstPrompt,omitempty"`
	Counts           Counts     `json:"counts"`
	Usage            Usage      `json:"usage"`
	Digest           []Digest   `json:"digest"`
	Todos            []Todo     `json:"todos"`
	FileEdits        []FileEdit `json:"fileEdits"`
	Commits          []string   `json:"commits"`
	// CommitSHAs is optional for the additive v1 rollout. When supplied, each
	// value is a resolved lowercase full Git object ID (SHA-1 or SHA-256).
	CommitSHAs []string   `json:"commitShas,omitempty"`
	Git        *GitDrift  `json:"git,omitempty"`
	Subagents  []Subagent `json:"subagents"`
}

type Counts struct {
	EditedFiles  int `json:"editedFiles"`
	Turns        int `json:"turns"`
	ToolUses     int `json:"toolUses"`
	Errors       int `json:"errors"`
	Compactions  int `json:"compactions"`
	Commands     int `json:"commands"`
	PullRequests int `json:"pullRequests"`
}

type Usage struct {
	Models                []ModelUsage `json:"models"`
	EstimatedCostMicroUSD int64        `json:"estimatedCostMicroUsd"`
	UnpricedModels        []string     `json:"unpricedModels"`
}

type ModelUsage struct {
	Model                      string `json:"model"`
	InputTokens                int    `json:"inputTokens"`
	OutputTokens               int    `json:"outputTokens"`
	CacheCreationInputTokens   int    `json:"cacheCreationInputTokens"`
	CacheCreation1hInputTokens int    `json:"cacheCreation1hInputTokens"`
	CacheReadInputTokens       int    `json:"cacheReadInputTokens"`
	EstimatedCostMicroUSD      int64  `json:"estimatedCostMicroUsd"`
}

type Digest struct {
	Turn        int     `json:"turn"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Answer      *string `json:"answer,omitempty"`
	SubagentID  *string `json:"subagentId,omitempty"`
}

type Todo struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type FileEdit struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Edits     int    `json:"edits"`
	IsNew     bool   `json:"isNew"`
}

type GitDrift struct {
	BaseBranch string `json:"baseBranch"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
}

type Subagent struct {
	ID                    string       `json:"id"`
	Name                  string       `json:"name"`
	Model                 *string      `json:"model,omitempty"`
	Status                string       `json:"status"`
	Task                  string       `json:"task"`
	Result                string       `json:"result"`
	DurationMs            *int         `json:"durationMs,omitempty"`
	SpawnedAtTurn         *int         `json:"spawnedAtTurn,omitempty"`
	ToolUses              int          `json:"toolUses"`
	CommandLabels         []string     `json:"commandLabels"`
	Usage                 []ModelUsage `json:"usage"`
	EstimatedCostMicroUSD int64        `json:"estimatedCostMicroUsd"`
}

// Marshal creates the canonical wire bytes and freezes ContentHash. Object
// member order is the struct order above, maps are forbidden from the wire
// types, strings use encoding/json escaping, and no insignificant whitespace
// or trailing newline is emitted.
func Marshal(snapshot Snapshot) ([]byte, error) {
	data, err := canonicalBytes(snapshot)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxPayloadBytes {
		return nil, fmt.Errorf("snapshot is %d bytes; maximum is %d: %w", len(data), MaxPayloadBytes, ErrOversized)
	}
	return data, nil
}

// Size returns the canonical payload size without returning content bytes or
// applying the aggregate ceiling. It exists for approved, content-free corpus
// measurement; Marshal remains the only API that returns uploadable bytes.
func Size(snapshot Snapshot) (int, error) {
	data, err := canonicalBytes(snapshot)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func canonicalBytes(snapshot Snapshot) ([]byte, error) {
	snapshot.ContentHash = ""
	if err := Validate(snapshot, false); err != nil {
		return nil, err
	}
	unsigned, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal unsigned snapshot: %w", err)
	}
	sum := sha256.Sum256(unsigned)
	snapshot.ContentHash = "sha256:" + hex.EncodeToString(sum[:])
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	return data, nil
}

// Decode accepts canonical v1 bytes only, rejects unknown fields, and verifies
// both the content hash and aggregate size.
func Decode(data []byte) (Snapshot, error) {
	if len(data) > MaxPayloadBytes {
		return Snapshot{}, fmt.Errorf("snapshot is %d bytes; maximum is %d: %w", len(data), MaxPayloadBytes, ErrOversized)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Snapshot{}, err
	}
	if err := Validate(snapshot, true); err != nil {
		return Snapshot{}, err
	}
	providedHash := snapshot.ContentHash
	snapshot.ContentHash = ""
	unsigned, err := json.Marshal(snapshot)
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal unsigned snapshot: %w", err)
	}
	sum := sha256.Sum256(unsigned)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])
	if providedHash != wantHash {
		return Snapshot{}, fmt.Errorf("contentHash does not match canonical payload: %w", ErrBadHash)
	}
	snapshot.ContentHash = providedHash
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal canonical snapshot: %w", err)
	}
	if !bytes.Equal(data, canonical) {
		return Snapshot{}, ErrNonCanonical
	}
	return snapshot, nil
}

var (
	ErrBadHash      = errors.New("invalid content hash")
	ErrNonCanonical = errors.New("snapshot JSON is not canonical")
	ErrOversized    = errors.New("snapshot exceeds aggregate limit")
)

func Validate(s Snapshot, requireHash bool) error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion must be %q", SchemaVersion)
	}
	if s.MediaType != MediaType {
		return fmt.Errorf("mediaType must be %q", MediaType)
	}
	if err := boundedRequired("collectorVersion", s.CollectorVersion, MaxCollectorVersionBytes); err != nil {
		return err
	}
	if !validAgent(s.Agent) {
		return fmt.Errorf("agent must be a lowercase identifier of at most %d bytes", MaxAgentBytes)
	}
	if err := boundedRequired("sourceSessionId", s.SourceSessionID, MaxIdentifierBytes); err != nil {
		return err
	}
	if s.SessionStartedAtMs <= 0 {
		return fmt.Errorf("sessionStartedAtMs must be positive")
	}
	if requireHash && (len(s.ContentHash) != 71 || !strings.HasPrefix(s.ContentHash, "sha256:")) {
		return fmt.Errorf("contentHash must be sha256 plus 64 lowercase hexadecimal characters: %w", ErrBadHash)
	}
	if requireHash {
		if _, err := hex.DecodeString(strings.TrimPrefix(s.ContentHash, "sha256:")); err != nil || strings.ToLower(s.ContentHash) != s.ContentHash {
			return ErrBadHash
		}
	}
	if err := boundedRequired("repository.canonical", s.Repository.Canonical, MaxRepositoryBytes); err != nil {
		return err
	}
	if s.Truncation == nil || s.Redactions == nil {
		return fmt.Errorf("truncation and redactions must be arrays")
	}
	if len(s.Truncation) > 5000 || len(s.Redactions) > 5000 {
		return fmt.Errorf("metadata arrays exceed limit")
	}
	for _, item := range s.Truncation {
		if !validJSONPointer(item.Path) {
			return fmt.Errorf("truncation path must be a JSON pointer")
		}
		if item.Reason != TruncationReasonTextBudget && item.Reason != TruncationReasonItemBudget && item.Reason != TruncationReasonAggregateBudget {
			return fmt.Errorf("unknown truncation reason")
		}
		if item.OriginalBytes != nil && *item.OriginalBytes < 0 ||
			item.ExportedBytes != nil && *item.ExportedBytes < 0 ||
			item.OriginalItems != nil && *item.OriginalItems < 0 ||
			item.ExportedItems != nil && *item.ExportedItems < 0 {
			return fmt.Errorf("truncation counters must be non-negative")
		}
	}
	for _, item := range s.Redactions {
		if !validJSONPointer(item.Path) {
			return fmt.Errorf("redaction path must be a JSON pointer")
		}
		if item.Reason == "" {
			return fmt.Errorf("redaction reason is required")
		}
	}
	if err := validateSession(s.Session); err != nil {
		return err
	}
	if s.SessionStartedAtMs > s.Session.LastActivityAtMs {
		return fmt.Errorf("sessionStartedAtMs must not exceed session.lastActivityAtMs")
	}
	return validateMetadataTargets(s)
}

func validateSession(s Session) error {
	fields := []struct {
		name  string
		value *string
		limit int
	}{
		{"name", s.Name, MaxNameBytes},
		{"summary", s.Summary, MaxSummaryBytes},
		{"status", s.Status, 64},
		{"cwd", s.WorkingDirectory, MaxPathBytes},
		{"branch", s.Branch, MaxBranchBytes},
		{"entrypoint", s.Entrypoint, MaxEntrypointBytes},
		{"declaredGoal", s.DeclaredGoal, MaxGoalBytes},
		{"firstPrompt", s.FirstPrompt, MaxPromptBytes},
	}
	for _, field := range fields {
		if field.value != nil {
			if err := bounded("session."+field.name, *field.value, field.limit); err != nil {
				return err
			}
		}
	}
	if s.Model != nil {
		if err := boundedRequired("session.model", *s.Model, MaxModelBytes); err != nil {
			return err
		}
	}
	if s.WorkingDirectory != nil && *s.WorkingDirectory != "." && !validRelativePath(*s.WorkingDirectory) {
		return fmt.Errorf("session.cwd must be repository-relative")
	}
	if s.LastActivityAtMs < 0 {
		return fmt.Errorf("session.lastActivityAtMs must be non-negative")
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
			return fmt.Errorf("session.counts.%s must be non-negative", count.name)
		}
	}
	if s.DurationMs != nil && *s.DurationMs < 0 {
		return fmt.Errorf("session.durationMs must be non-negative")
	}
	if s.LastEditAtMs != nil && *s.LastEditAtMs < 0 {
		return fmt.Errorf("session.lastEditAtMs must be non-negative")
	}
	if s.ContextTokens != nil && *s.ContextTokens < 0 {
		return fmt.Errorf("session.contextTokens must be non-negative")
	}
	if s.ContextWindow != nil && *s.ContextWindow < 0 {
		return fmt.Errorf("session.contextWindow must be non-negative")
	}
	if s.Usage.EstimatedCostMicroUSD < 0 {
		return fmt.Errorf("session usage cost must be non-negative")
	}
	if s.Usage.Models == nil || s.Usage.UnpricedModels == nil || s.Digest == nil || s.Todos == nil || s.FileEdits == nil || s.Commits == nil || s.Subagents == nil {
		return fmt.Errorf("collection fields must be arrays, not null")
	}
	if len(s.Usage.Models) > MaxUsageModels || len(s.Usage.UnpricedModels) > MaxUnpricedModels || len(s.Digest) > MaxDigestItems || len(s.Todos) > MaxTodoItems || len(s.FileEdits) > MaxFileEditItems || len(s.Commits) > MaxCommitItems || len(s.Subagents) > MaxSubagentItems {
		return fmt.Errorf("session collection exceeds item budget")
	}
	if !sortedUniqueUsage(s.Usage.Models) || !sortedUniqueStrings(s.Usage.UnpricedModels) {
		return fmt.Errorf("usage model lists must be sorted and unique")
	}
	for _, usage := range s.Usage.Models {
		if err := validateUsage(usage); err != nil {
			return err
		}
	}
	for i, model := range s.Usage.UnpricedModels {
		if err := boundedRequired(fmt.Sprintf("session.usage.unpricedModels[%d]", i), model, MaxModelBytes); err != nil {
			return err
		}
	}
	for i, digest := range s.Digest {
		if digest.Turn < 0 {
			return fmt.Errorf("session.digest[%d].turn must be non-negative", i)
		}
		if err := boundedRequired(fmt.Sprintf("session.digest[%d].category", i), digest.Category, 64); err != nil {
			return err
		}
		if err := bounded(fmt.Sprintf("session.digest[%d].description", i), digest.Description, MaxDigestTextBytes); err != nil {
			return err
		}
		if digest.Answer != nil {
			if err := bounded(fmt.Sprintf("session.digest[%d].answer", i), *digest.Answer, MaxDigestTextBytes); err != nil {
				return err
			}
		}
		if digest.SubagentID != nil {
			if err := boundedRequired(fmt.Sprintf("session.digest[%d].subagentId", i), *digest.SubagentID, MaxIdentifierBytes); err != nil {
				return err
			}
		}
	}
	for i, todo := range s.Todos {
		if err := bounded(fmt.Sprintf("session.todos[%d].text", i), todo.Text, MaxTodoTextBytes); err != nil {
			return err
		}
	}
	for _, edit := range s.FileEdits {
		if !validRelativePath(edit.Path) || len(edit.Path) > MaxPathBytes || edit.Additions < 0 || edit.Deletions < 0 || edit.Edits < 0 {
			return fmt.Errorf("invalid repository-relative file edit")
		}
	}
	for i, commit := range s.Commits {
		if err := bounded(fmt.Sprintf("session.commits[%d]", i), commit, MaxCommitTextBytes); err != nil {
			return err
		}
	}
	if len(s.CommitSHAs) > MaxCommitSHAItems {
		return fmt.Errorf("session.commitShas exceeds item budget")
	}
	seenCommitSHAs := make(map[string]struct{}, len(s.CommitSHAs))
	for i, sha := range s.CommitSHAs {
		if !validCommitSHA(sha) {
			return fmt.Errorf("session.commitShas[%d] must be a lowercase full Git object ID", i)
		}
		if _, duplicate := seenCommitSHAs[sha]; duplicate {
			return fmt.Errorf("session.commitShas must be unique")
		}
		seenCommitSHAs[sha] = struct{}{}
	}
	if s.Git != nil {
		if err := boundedRequired("session.git.baseBranch", s.Git.BaseBranch, MaxBranchBytes); err != nil {
			return err
		}
		if s.Git.Ahead < 0 || s.Git.Behind < 0 {
			return fmt.Errorf("git drift counts must be non-negative")
		}
	}
	commandLabels := 0
	for i, subagent := range s.Subagents {
		if subagent.ToolUses < 0 || subagent.EstimatedCostMicroUSD < 0 || subagent.CommandLabels == nil || subagent.Usage == nil {
			return fmt.Errorf("invalid subagent")
		}
		if err := boundedRequired(fmt.Sprintf("session.subagents[%d].id", i), subagent.ID, MaxIdentifierBytes); err != nil {
			return err
		}
		if err := boundedRequired(fmt.Sprintf("session.subagents[%d].name", i), subagent.Name, MaxNameBytes); err != nil {
			return err
		}
		if subagent.Model != nil {
			if err := boundedRequired(fmt.Sprintf("session.subagents[%d].model", i), *subagent.Model, MaxModelBytes); err != nil {
				return err
			}
		}
		if err := boundedRequired(fmt.Sprintf("session.subagents[%d].status", i), subagent.Status, 64); err != nil {
			return err
		}
		if err := bounded(fmt.Sprintf("session.subagents[%d].task", i), subagent.Task, MaxSubagentTextBytes); err != nil {
			return err
		}
		if err := bounded(fmt.Sprintf("session.subagents[%d].result", i), subagent.Result, MaxSubagentTextBytes); err != nil {
			return err
		}
		if subagent.DurationMs != nil && *subagent.DurationMs < 0 {
			return fmt.Errorf("subagent duration must be non-negative")
		}
		if subagent.SpawnedAtTurn != nil && *subagent.SpawnedAtTurn < 0 {
			return fmt.Errorf("subagent spawn turn must be non-negative")
		}
		commandLabels += len(subagent.CommandLabels)
		for j, label := range subagent.CommandLabels {
			if err := boundedRequired(fmt.Sprintf("session.subagents[%d].commandLabels[%d]", i, j), label, MaxCommandLabelBytes); err != nil {
				return err
			}
		}
		if len(subagent.Usage) > MaxUsageModels || !sortedUniqueUsage(subagent.Usage) {
			return fmt.Errorf("subagent usage must be sorted and unique")
		}
		for _, usage := range subagent.Usage {
			if err := validateUsage(usage); err != nil {
				return err
			}
		}
	}
	if commandLabels > MaxCommandLabelItems {
		return fmt.Errorf("subagent command labels exceed item budget")
	}
	return nil
}

func validCommitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func validateUsage(u ModelUsage) error {
	if boundedRequired("usage.model", u.Model, MaxModelBytes) != nil || u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheCreationInputTokens < 0 || u.CacheCreation1hInputTokens < 0 || u.CacheReadInputTokens < 0 || u.EstimatedCostMicroUSD < 0 {
		return fmt.Errorf("model usage fields must be non-negative and model is required")
	}
	return nil
}

func boundedRequired(name, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return fmt.Errorf("%s exceeds its UTF-8 byte budget", name)
	}
	return nil
}

func bounded(name, value string, maxBytes int) error {
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return fmt.Errorf("%s exceeds its UTF-8 byte budget", name)
	}
	return nil
}

func validRelativePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." || part == "" {
			return false
		}
	}
	return true
}

func validAgent(value string) bool {
	if value == "" || len(value) > MaxAgentBytes {
		return false
	}
	for i := range len(value) {
		char := value[i]
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
		if i == 0 && (char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func validJSONPointer(path string) bool {
	if !strings.HasPrefix(path, "/") || !utf8.ValidString(path) {
		return false
	}
	for i := 0; i < len(path); i++ {
		if path[i] != '~' {
			continue
		}
		if i+1 >= len(path) || (path[i+1] != '0' && path[i+1] != '1') {
			return false
		}
		i++
	}
	return true
}

func validateMetadataTargets(snapshot Snapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	for _, item := range snapshot.Truncation {
		if !jsonPointerResolves(document, item.Path) {
			return fmt.Errorf("truncation path does not resolve: %s", item.Path)
		}
	}
	for _, item := range snapshot.Redactions {
		if !jsonPointerResolves(document, item.Path) {
			return fmt.Errorf("redaction path does not resolve: %s", item.Path)
		}
	}
	return nil
}

func jsonPointerResolves(value any, pointer string) bool {
	for _, encoded := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		segment := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[segment]
			if !ok {
				return false
			}
		case []any:
			if segment == "" || len(segment) > 1 && segment[0] == '0' {
				return false
			}
			for i := range len(segment) {
				if segment[i] < '0' || segment[i] > '9' {
					return false
				}
			}
			index, err := strconv.Atoi(segment)
			if err != nil || index >= len(current) {
				return false
			}
			value = current[index]
		default:
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

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("snapshot contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return nil
}

// CostMicroUSD freezes a floating-point local estimate at the wire boundary.
func CostMicroUSD(dollars float64) (int64, error) {
	if math.IsNaN(dollars) || math.IsInf(dollars, 0) || dollars < 0 || dollars >= float64(math.MaxInt64)/1_000_000 {
		return 0, fmt.Errorf("estimated cost is not a finite non-negative value")
	}
	return int64(math.Round(dollars * 1_000_000)), nil
}
