// Package fullsessionv1 defines the storage-neutral complete parsed-session
// record shared by SSH collection, the local cache, and downstream v2 work.
// It contains parsed product data, never raw vendor transcript rows.
package fullsessionv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion  = "full-session-record/v1"
	MaxRecordBytes = 64 << 20
	MaxTextBytes   = 32 << 20
	MaxStringBytes = 1 << 20
	MaxItems       = 100_000

	// MaxSessionTimestampMs is the last millisecond representable in year 9999.
	// Downstream consumers persist these values as PostgreSQL timestamptz, so
	// reject larger values before storage-specific conversion can overflow.
	MaxSessionTimestampMs int64 = 253_402_300_799_999
)

var (
	ErrInvalid   = errors.New("invalid full session record")
	ErrOversized = errors.New("full session record exceeds its byte limit")
)

type Record struct {
	SchemaVersion   string  `json:"schemaVersion"`
	SourceID        string  `json:"sourceId"`
	Agent           string  `json:"agent"`
	SessionID       string  `json:"sessionId"`
	ParentSessionID string  `json:"parentSessionId"`
	RevisionID      string  `json:"revisionId"`
	Session         Session `json:"session"`
}

type Session struct {
	Name             *string           `json:"name"`
	Summary          *string           `json:"summary"`
	Status           *string           `json:"status"`
	WorkingDirectory string            `json:"cwd"`
	Branch           *string           `json:"branch"`
	EditedFileCount  int               `json:"editedFileCount"`
	DurationMs       *int              `json:"durationMs"`
	Usage            []ModelUsage      `json:"usage"`
	CostMicroUSD     *int64            `json:"costMicroUsd"`
	UnpricedModels   []string          `json:"unpricedModels"`
	Subagents        []Subagent        `json:"subagents"`
	StartedAtMs      int64             `json:"startedAtMs"`
	LastActivityAtMs int64             `json:"lastActivityAtMs"`
	Entrypoint       *string           `json:"entrypoint"`
	Model            *string           `json:"model"`
	ContextTokens    *int              `json:"contextTokens"`
	ContextWindow    *int              `json:"contextWindow"`
	Turns            int               `json:"turns"`
	ToolUses         int               `json:"toolUses"`
	Errors           int               `json:"errors"`
	Compactions      int               `json:"compactions"`
	FirstPrompt      *string           `json:"firstPrompt"`
	Commands         []string          `json:"commands"`
	Commits          []string          `json:"commits"`
	CommitSHAs       []string          `json:"commitShas"`
	PullRequests     int               `json:"pullRequests"`
	Todos            []Todo            `json:"todos"`
	Digest           []DigestEntry     `json:"digest"`
	FileEdits        []FileEdit        `json:"fileEdits"`
	Synthesis        *SessionSynthesis `json:"synthesis"`
	SynthesisPending bool              `json:"synthesisPending"`
	DeclaredGoal     *string           `json:"declaredGoal"`
}

type ModelUsage struct {
	Model                      string `json:"model"`
	InputTokens                int    `json:"inputTokens"`
	OutputTokens               int    `json:"outputTokens"`
	CacheCreationInputTokens   int    `json:"cacheCreationInputTokens"`
	CacheCreation1hInputTokens int    `json:"cacheCreation1hInputTokens"`
	CacheReadInputTokens       int    `json:"cacheReadInputTokens"`
	CostMicroUSD               int64  `json:"costMicroUsd"`
}

type SubagentCommand struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type Subagent struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Model         *string           `json:"model"`
	Status        string            `json:"status"`
	Task          string            `json:"task"`
	Result        string            `json:"result"`
	DurationMs    *int              `json:"durationMs"`
	SpawnedAtTurn *int              `json:"spawnedAtTurn"`
	ToolUses      int               `json:"toolUses"`
	Commands      []SubagentCommand `json:"commands"`
	Usage         []ModelUsage      `json:"usage"`
	CostMicroUSD  *int64            `json:"costMicroUsd"`
}

type Todo struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type DigestEntry struct {
	Turn        int    `json:"turn"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Answer      string `json:"answer"`
	SubagentID  string `json:"subagentId"`
	TimeMs      int64  `json:"timeMs"`
}

type FileEdit struct {
	Path      string       `json:"path"`
	Additions int          `json:"additions"`
	Deletions int          `json:"deletions"`
	Edits     int          `json:"edits"`
	IsNew     bool         `json:"isNew"`
	Changes   []FileChange `json:"changes"`
}

type FileChange struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Operation string `json:"operation"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	ByteCount int    `json:"byteCount"`
	SHA256    string `json:"sha256"`
}

type SessionSynthesis struct {
	Goals        []string `json:"goals"`
	Outcome      string   `json:"outcome"`
	KeyDecisions []string `json:"keyDecisions"`
	NextStep     string   `json:"nextStep"`
}

// Freeze computes all body hashes/counts and the immutable revision identity.
// The revision is the SHA-256 of the canonical record with revisionId empty.
func Freeze(record Record) (Record, error) {
	record = cloneRecord(record)
	record.SchemaVersion = SchemaVersion
	record.RevisionID = ""
	for editIndex := range record.Session.FileEdits {
		for changeIndex := range record.Session.FileEdits[editIndex].Changes {
			change := &record.Session.FileEdits[editIndex].Changes[changeIndex]
			if change.ID == "" {
				change.ID = fmt.Sprintf("change-%06d-%06d", editIndex, changeIndex)
			}
			change.ByteCount = len(change.Text)
			digest := sha256.Sum256([]byte(change.Text))
			change.SHA256 = hex.EncodeToString(digest[:])
		}
	}
	if err := validate(record, false); err != nil {
		return Record{}, err
	}
	preimage, err := json.Marshal(record)
	if err != nil {
		return Record{}, err
	}
	digest := sha256.Sum256(preimage)
	record.RevisionID = hex.EncodeToString(digest[:])
	data, err := json.Marshal(record)
	if err != nil {
		return Record{}, err
	}
	if len(data) > MaxRecordBytes {
		return Record{}, ErrOversized
	}
	return record, nil
}

func cloneRecord(record Record) Record {
	cloned := record
	s := record.Session
	s.Name = clonePointer(s.Name)
	s.Summary = clonePointer(s.Summary)
	s.Status = clonePointer(s.Status)
	s.Branch = clonePointer(s.Branch)
	s.DurationMs = clonePointer(s.DurationMs)
	s.CostMicroUSD = clonePointer(s.CostMicroUSD)
	s.Entrypoint = clonePointer(s.Entrypoint)
	s.Model = clonePointer(s.Model)
	s.ContextTokens = clonePointer(s.ContextTokens)
	s.ContextWindow = clonePointer(s.ContextWindow)
	s.FirstPrompt = clonePointer(s.FirstPrompt)
	s.DeclaredGoal = clonePointer(s.DeclaredGoal)
	s.Usage = cloneSlice(s.Usage)
	s.UnpricedModels = cloneSlice(s.UnpricedModels)
	s.Commands = cloneSlice(s.Commands)
	s.Commits = cloneSlice(s.Commits)
	s.CommitSHAs = cloneSlice(s.CommitSHAs)
	s.Todos = cloneSlice(s.Todos)
	s.Digest = cloneSlice(s.Digest)
	s.Subagents = cloneSlice(s.Subagents)
	for i := range s.Subagents {
		s.Subagents[i].Model = clonePointer(s.Subagents[i].Model)
		s.Subagents[i].DurationMs = clonePointer(s.Subagents[i].DurationMs)
		s.Subagents[i].SpawnedAtTurn = clonePointer(s.Subagents[i].SpawnedAtTurn)
		s.Subagents[i].CostMicroUSD = clonePointer(s.Subagents[i].CostMicroUSD)
		s.Subagents[i].Commands = cloneSlice(s.Subagents[i].Commands)
		s.Subagents[i].Usage = cloneSlice(s.Subagents[i].Usage)
	}
	s.FileEdits = cloneSlice(s.FileEdits)
	for i := range s.FileEdits {
		s.FileEdits[i].Changes = cloneSlice(s.FileEdits[i].Changes)
	}
	if s.Synthesis != nil {
		value := *s.Synthesis
		value.Goals = cloneSlice(value.Goals)
		value.KeyDecisions = cloneSlice(value.KeyDecisions)
		s.Synthesis = &value
	}
	cloned.Session = s
	return cloned
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	return append(make([]T, 0, len(value)), value...)
}

func Marshal(record Record) ([]byte, error) {
	if err := Validate(record); err != nil {
		return nil, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRecordBytes {
		return nil, ErrOversized
	}
	return data, nil
}

func Decode(data []byte) (Record, error) {
	if len(data) > MaxRecordBytes {
		return Record{}, ErrOversized
	}
	if err := validateCollectionSizes(data); err != nil {
		return Record{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record Record
	if err := decoder.Decode(&record); err != nil {
		return Record{}, fmt.Errorf("%w: decode: %v", ErrInvalid, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Record{}, fmt.Errorf("%w: trailing JSON value", ErrInvalid)
	}
	if err := Validate(record); err != nil {
		return Record{}, err
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(canonical, data) {
		return Record{}, fmt.Errorf("%w: non-canonical JSON", ErrInvalid)
	}
	return record, nil
}

// DecodeReader reads and decodes one record without allowing the input source
// to allocate beyond the record byte limit.
func DecodeReader(reader io.Reader) (Record, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxRecordBytes+1))
	if err != nil {
		return Record{}, fmt.Errorf("read full session record: %w", err)
	}
	return Decode(data)
}

func validateCollectionSizes(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	type container struct {
		kind  json.Delim
		items int
	}
	stack := []container{}
	totalItems := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: decode: %v", ErrInvalid, err)
		}
		delim, isDelim := token.(json.Delim)
		if isDelim && (delim == ']' || delim == '}') {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if len(stack) > 0 && stack[len(stack)-1].kind == '[' {
			stack[len(stack)-1].items++
			totalItems++
			if stack[len(stack)-1].items > MaxItems || totalItems > MaxItems {
				return fmt.Errorf("%w: collection exceeds item limit", ErrInvalid)
			}
		}
		if isDelim && (delim == '[' || delim == '{') {
			if len(stack) >= MaxItems {
				return fmt.Errorf("%w: collection nesting exceeds item limit", ErrInvalid)
			}
			stack = append(stack, container{kind: delim})
		}
	}
}

func Validate(record Record) error {
	if err := validate(record, true); err != nil {
		return err
	}
	copy := record
	copy.RevisionID = ""
	preimage, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(preimage)
	if record.RevisionID != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("%w: revision hash mismatch", ErrInvalid)
	}
	return nil
}

func validate(record Record, requireRevision bool) error {
	if record.SchemaVersion != SchemaVersion || !identifier(record.SourceID) ||
		(record.Agent != "codex" && record.Agent != "claude") || !identifier(record.SessionID) ||
		(record.ParentSessionID != "" && (!identifier(record.ParentSessionID) || record.ParentSessionID == record.SessionID)) {
		return fmt.Errorf("%w: invalid envelope", ErrInvalid)
	}
	if requireRevision && !digest(record.RevisionID) {
		return fmt.Errorf("%w: invalid revision", ErrInvalid)
	}
	if !requireRevision && record.RevisionID != "" {
		return fmt.Errorf("%w: revision must be empty while freezing", ErrInvalid)
	}
	s := record.Session
	if s.StartedAtMs <= 0 || s.StartedAtMs > MaxSessionTimestampMs ||
		s.LastActivityAtMs < s.StartedAtMs || s.LastActivityAtMs > MaxSessionTimestampMs || !optionalInt64Nonnegative(s.CostMicroUSD) ||
		!nonnegative(s.EditedFileCount, s.Turns, s.ToolUses, s.Errors, s.Compactions, s.PullRequests) ||
		!optionalNonnegative(s.DurationMs, s.ContextTokens, s.ContextWindow) {
		return fmt.Errorf("%w: invalid session counts or time", ErrInvalid)
	}
	if !stringsValid(s.WorkingDirectory, stringValue(s.Name), stringValue(s.Summary), stringValue(s.Status),
		stringValue(s.Branch), stringValue(s.Entrypoint), stringValue(s.Model),
		stringValue(s.FirstPrompt), stringValue(s.DeclaredGoal)) {
		return fmt.Errorf("%w: invalid session text", ErrInvalid)
	}
	if !boundedItems(len(s.Usage), len(s.UnpricedModels), len(s.Subagents), len(s.Commands), len(s.Commits),
		len(s.CommitSHAs), len(s.Todos), len(s.Digest), len(s.FileEdits)) {
		return fmt.Errorf("%w: session collection exceeds item limit", ErrInvalid)
	}
	if s.EditedFileCount != len(s.FileEdits) {
		return fmt.Errorf("%w: edited file count does not match file edits", ErrInvalid)
	}
	previousModel := ""
	for index, usage := range s.Usage {
		if !validUsage(usage) || (index > 0 && usage.Model <= previousModel) {
			return fmt.Errorf("%w: invalid or unsorted usage", ErrInvalid)
		}
		previousModel = usage.Model
	}
	if !stringSliceValid(s.UnpricedModels) || !stringSliceValid(s.Commands) ||
		!stringSliceValid(s.Commits) || !stringSliceValid(s.CommitSHAs) {
		return fmt.Errorf("%w: invalid session list text", ErrInvalid)
	}
	for _, item := range s.Todos {
		if !stringsValid(item.Text) {
			return fmt.Errorf("%w: invalid todo", ErrInvalid)
		}
	}
	for _, item := range s.Digest {
		if item.Turn < 0 || item.TimeMs < 0 || !stringsValid(item.Category, item.Description, item.Answer, item.SubagentID) {
			return fmt.Errorf("%w: invalid digest", ErrInvalid)
		}
	}
	seenChanges := map[string]bool{}
	for _, edit := range s.FileEdits {
		if edit.Path == "" || !stringsValid(edit.Path) || !nonnegative(edit.Additions, edit.Deletions) || edit.Edits <= 0 || !boundedItems(len(edit.Changes)) {
			return fmt.Errorf("%w: invalid file edit", ErrInvalid)
		}
		for _, change := range edit.Changes {
			if !identifier(change.ID) || seenChanges[change.ID] ||
				(change.Kind != "diff" && change.Kind != "content") || change.Operation == "" || !stringsValid(change.Operation) ||
				!utf8.ValidString(change.Text) || len(change.Text) > MaxTextBytes ||
				change.ByteCount != len(change.Text) || !nonnegative(change.Additions, change.Deletions) {
				return fmt.Errorf("%w: invalid file change", ErrInvalid)
			}
			bodyHash := sha256.Sum256([]byte(change.Text))
			if change.SHA256 != hex.EncodeToString(bodyHash[:]) {
				return fmt.Errorf("%w: file change hash mismatch", ErrInvalid)
			}
			seenChanges[change.ID] = true
		}
	}
	for _, subagent := range s.Subagents {
		if !identifier(subagent.ID) || !stringsValid(subagent.Name, stringValue(subagent.Model), subagent.Status, subagent.Task, subagent.Result) ||
			!optionalNonnegative(subagent.DurationMs, subagent.SpawnedAtTurn) || subagent.ToolUses < 0 || !optionalInt64Nonnegative(subagent.CostMicroUSD) ||
			!boundedItems(len(subagent.Commands), len(subagent.Usage)) {
			return fmt.Errorf("%w: invalid subagent", ErrInvalid)
		}
		for _, command := range subagent.Commands {
			if !stringsValid(command.Label, command.Command) {
				return fmt.Errorf("%w: invalid subagent command", ErrInvalid)
			}
		}
		previousModel := ""
		for index, usage := range subagent.Usage {
			if !validUsage(usage) || (index > 0 && usage.Model <= previousModel) {
				return fmt.Errorf("%w: invalid or unsorted subagent usage", ErrInvalid)
			}
			previousModel = usage.Model
		}
	}
	if s.Synthesis != nil && (!stringSliceValid(s.Synthesis.Goals) || !stringSliceValid(s.Synthesis.KeyDecisions) ||
		!stringsValid(s.Synthesis.Outcome, s.Synthesis.NextStep)) {
		return fmt.Errorf("%w: invalid synthesis", ErrInvalid)
	}
	return nil
}

func validUsage(usage ModelUsage) bool {
	return stringsValid(usage.Model) && usage.Model != "" && usage.CostMicroUSD >= 0 &&
		nonnegative(usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens,
			usage.CacheCreation1hInputTokens, usage.CacheReadInputTokens)
}

func identifier(value string) bool {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || !stringsValid(value) || strings.ContainsAny(value, "/\\") {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
func digest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func stringsValid(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) || len(value) > MaxStringBytes {
			return false
		}
	}
	return true
}
func stringSliceValid(values []string) bool {
	if !boundedItems(len(values)) {
		return false
	}
	return stringsValid(values...)
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func boundedItems(values ...int) bool {
	for _, value := range values {
		if value < 0 || value > MaxItems {
			return false
		}
	}
	return true
}
func nonnegative(values ...int) bool {
	for _, value := range values {
		if value < 0 {
			return false
		}
	}
	return true
}
func optionalNonnegative(values ...*int) bool {
	for _, value := range values {
		if value != nil && *value < 0 {
			return false
		}
	}
	return true
}

func optionalInt64Nonnegative(value *int64) bool {
	return value == nil || *value >= 0
}
