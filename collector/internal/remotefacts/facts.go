// Package remotefacts defines the versioned, transport-independent facts used
// to compose remote session families. It performs no filesystem, SSH, or cache
// I/O.
package remotefacts

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const (
	SchemaVersion = 1

	MaxIDBytes          = 200
	MaxDisplayBytes     = 280
	MaxModelBytes       = 120
	MaxSessions         = 256
	MaxFingerprints     = 512
	MaxHeaderMappings   = 512
	MaxModelsPerSession = 32
	MaxSpawnsPerSession = 256
	MaxCommands         = 128
	MaxNesting          = 8
	MaxCount            = 1_000_000_000
	MaxCostMicroUSD     = int64(1_000_000_000_000_000)
	MaxTimestampMs      = int64(32_503_680_000_000) // year 3000
)

const (
	StateComplete = "complete"
	StatePartial  = "partial"
	StateStale    = "stale"
)

// Family is the complete normalized replacement unit. Paths and raw transcript
// content deliberately have no representation in this type.
type Family struct {
	SchemaVersion  int             `json:"schema_version"`
	ParserVersion  string          `json:"parser_version"`
	Vendor         string          `json:"vendor"`
	FamilyID       string          `json:"family_id"`
	State          string          `json:"state"`
	StaleReason    string          `json:"stale_reason,omitempty"`
	Sessions       []Session       `json:"sessions"`
	Metadata       Metadata        `json:"metadata"`
	Fingerprints   []Fingerprint   `json:"fingerprints"`
	HeaderMappings []HeaderMapping `json:"header_mappings,omitempty"`
}

type Session struct {
	ID                 string       `json:"id"`
	ParentID           string       `json:"parent_id,omitempty"`
	SpawnKey           string       `json:"spawn_key,omitempty"`
	Name               string       `json:"name,omitempty"`
	StatusHint         string       `json:"status_hint,omitempty"`
	Branch             string       `json:"branch,omitempty"`
	Entrypoint         string       `json:"entrypoint,omitempty"`
	StartedAtMs        int64        `json:"started_at_ms"`
	LastActivityAtMs   int64        `json:"last_activity_at_ms"`
	DurationMs         *int         `json:"duration_ms,omitempty"`
	Stopped            bool         `json:"stopped,omitempty"`
	InTurn             bool         `json:"in_turn,omitempty"`
	Model              string       `json:"model,omitempty"`
	ContextTokens      *int         `json:"context_tokens,omitempty"`
	ContextWindow      *int         `json:"context_window,omitempty"`
	Counts             Counts       `json:"counts"`
	Usage              []ModelUsage `json:"usage"`
	RecordedCostMicros *int64       `json:"recorded_cost_micro_usd,omitempty"`
	Spawns             []Spawn      `json:"spawns"`
	CommandLabels      []string     `json:"command_labels"`
}

type Counts struct {
	EditedFiles  int `json:"edited_files"`
	Turns        int `json:"turns"`
	ToolUses     int `json:"tool_uses"`
	Errors       int `json:"errors"`
	Compactions  int `json:"compactions"`
	PullRequests int `json:"pull_requests"`
}

type ModelUsage struct {
	Model                      string `json:"model"`
	InputTokens                int    `json:"input_tokens"`
	OutputTokens               int    `json:"output_tokens"`
	CacheCreationInputTokens   int    `json:"cache_creation_input_tokens"`
	CacheCreation1hInputTokens int    `json:"cache_creation_1h_input_tokens"`
	CacheReadInputTokens       int    `json:"cache_read_input_tokens"`
	CostMicroUSD               int64  `json:"cost_micro_usd"`
}

type Spawn struct {
	Key       string `json:"key"`
	Turn      *int   `json:"turn,omitempty"`
	Completed bool   `json:"completed"`
}

type Metadata struct {
	Names []MetadataName `json:"names"`
	Live  []MetadataLive `json:"live"`
}

type MetadataName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type MetadataLive struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type Fingerprint struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	ModifiedAtMs int64  `json:"modified_at_ms"`
}

// HeaderMapping is the privacy-safe part of a Codex rollout header needed to
// reconstruct a family without reopening an unchanged transcript. Key is the
// opaque fingerprint key, never a path.
type HeaderMapping struct {
	Key       string `json:"key"`
	SessionID string `json:"session_id"`
	ParentID  string `json:"parent_id,omitempty"`
}

func Validate(f Family) error {
	if f.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", f.SchemaVersion)
	}
	if f.ParserVersion == "" || !bounded(f.ParserVersion, MaxIDBytes) {
		return errors.New("invalid parser_version")
	}
	if f.Vendor != vendors.AgentClaude && f.Vendor != vendors.AgentCodex {
		return fmt.Errorf("invalid vendor %q", f.Vendor)
	}
	if !identifier(f.FamilyID) {
		return errors.New("invalid family_id")
	}
	if f.State != StateComplete && f.State != StatePartial && f.State != StateStale {
		return fmt.Errorf("invalid family state %q", f.State)
	}
	if f.State == StateStale && (f.StaleReason == "" || !bounded(f.StaleReason, MaxDisplayBytes)) {
		return errors.New("stale family requires a bounded reason")
	}
	if len(f.Sessions) == 0 || len(f.Sessions) > MaxSessions {
		return errors.New("invalid session count")
	}
	if len(f.Fingerprints) == 0 || len(f.Fingerprints) > MaxFingerprints {
		return errors.New("invalid fingerprint count")
	}
	seenSessions := map[string]bool{}
	for i, s := range f.Sessions {
		if err := validateSession(s); err != nil {
			return fmt.Errorf("sessions[%d]: %w", i, err)
		}
		if seenSessions[s.ID] {
			return fmt.Errorf("duplicate session id %q", s.ID)
		}
		seenSessions[s.ID] = true
	}
	if !seenSessions[f.FamilyID] {
		return errors.New("family_id has no root session")
	}
	parents := make(map[string]string, len(f.Sessions))
	for _, s := range f.Sessions {
		if s.ParentID != "" && !seenSessions[s.ParentID] {
			return fmt.Errorf("session %q has unknown parent", s.ID)
		}
		parents[s.ID] = s.ParentID
	}
	if parents[f.FamilyID] != "" {
		return errors.New("family_id session must be a root")
	}
	for id := range parents {
		seen := map[string]bool{}
		current := id
		for depth := 0; ; depth++ {
			if seen[current] {
				return fmt.Errorf("session %q has a parent cycle", id)
			}
			seen[current] = true
			if depth >= MaxNesting {
				return fmt.Errorf("session %q exceeds nesting limit", id)
			}
			parent := parents[current]
			if parent == "" {
				if current != f.FamilyID {
					return fmt.Errorf("session %q is not descended from family root", id)
				}
				break
			}
			current = parent
		}
	}
	if err := validateMetadata(f.Metadata, seenSessions); err != nil {
		return err
	}
	previous := ""
	fingerprintKeys := map[string]bool{}
	for i, fp := range f.Fingerprints {
		if !identifier(fp.Key) || fp.Size < 0 || fp.ModifiedAtMs < 0 || fp.ModifiedAtMs > MaxTimestampMs {
			return fmt.Errorf("fingerprints[%d] is invalid", i)
		}
		if fp.Key <= previous {
			return errors.New("fingerprints must be uniquely sorted by key")
		}
		previous = fp.Key
		fingerprintKeys[fp.Key] = true
	}
	if len(f.HeaderMappings) > MaxHeaderMappings {
		return errors.New("header mapping count exceeds limit")
	}
	previous = ""
	for i, mapping := range f.HeaderMappings {
		if !fingerprintKeys[mapping.Key] || !identifier(mapping.SessionID) ||
			!seenSessions[mapping.SessionID] ||
			(mapping.ParentID != "" && (!identifier(mapping.ParentID) || !seenSessions[mapping.ParentID])) ||
			mapping.Key <= previous {
			return fmt.Errorf("header_mappings[%d] is invalid or unsorted", i)
		}
		previous = mapping.Key
	}
	return nil
}

func validateSession(s Session) error {
	if !identifier(s.ID) || (s.ParentID != "" && !identifier(s.ParentID)) || (s.SpawnKey != "" && !identifier(s.SpawnKey)) {
		return errors.New("invalid identity")
	}
	for _, value := range []string{s.Name, s.StatusHint, s.Branch, s.Entrypoint} {
		if !bounded(value, MaxDisplayBytes) {
			return errors.New("display field exceeds limit")
		}
	}
	if !bounded(s.Model, MaxModelBytes) || s.StartedAtMs <= 0 || s.StartedAtMs > MaxTimestampMs || s.LastActivityAtMs < s.StartedAtMs || s.LastActivityAtMs > MaxTimestampMs {
		return errors.New("invalid timing or model")
	}
	if err := validateOptionalCount(s.DurationMs); err != nil {
		return err
	}
	if err := validateOptionalCount(s.ContextTokens); err != nil {
		return err
	}
	if err := validateOptionalCount(s.ContextWindow); err != nil {
		return err
	}
	if s.RecordedCostMicros != nil && (*s.RecordedCostMicros < 0 || *s.RecordedCostMicros > MaxCostMicroUSD) {
		return errors.New("recorded cost outside range")
	}
	counts := []int{s.Counts.EditedFiles, s.Counts.Turns, s.Counts.ToolUses, s.Counts.Errors, s.Counts.Compactions, s.Counts.PullRequests}
	for _, count := range counts {
		if count < 0 || count > MaxCount {
			return errors.New("count outside range")
		}
	}
	if len(s.Usage) > MaxModelsPerSession || len(s.Spawns) > MaxSpawnsPerSession || len(s.CommandLabels) > MaxCommands {
		return errors.New("list exceeds limit")
	}
	previous := ""
	for _, usage := range s.Usage {
		if usage.Model == "" || !bounded(usage.Model, MaxModelBytes) || usage.Model <= previous {
			return errors.New("usage must be uniquely sorted by model")
		}
		previous = usage.Model
		for _, count := range []int{usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheCreation1hInputTokens, usage.CacheReadInputTokens} {
			if count < 0 || count > MaxCount {
				return errors.New("token count outside range")
			}
		}
		if usage.CostMicroUSD < 0 || usage.CostMicroUSD > MaxCostMicroUSD {
			return errors.New("negative model cost")
		}
	}
	previous = ""
	for _, spawn := range s.Spawns {
		if !identifier(spawn.Key) || spawn.Key <= previous {
			return errors.New("spawns must be uniquely sorted by key")
		}
		previous = spawn.Key
		if err := validateOptionalCount(spawn.Turn); err != nil {
			return err
		}
	}
	for _, label := range s.CommandLabels {
		if !bounded(label, MaxDisplayBytes) {
			return errors.New("command label exceeds limit")
		}
	}
	return nil
}

func validateMetadata(m Metadata, sessions map[string]bool) error {
	previous := ""
	for _, value := range m.Names {
		if !sessions[value.ID] || value.ID <= previous || !bounded(value.Name, MaxDisplayBytes) {
			return errors.New("invalid or unsorted metadata name")
		}
		previous = value.ID
	}
	previous = ""
	for _, value := range m.Live {
		if !sessions[value.ID] || value.ID <= previous || !bounded(value.Status, MaxDisplayBytes) {
			return errors.New("invalid or unsorted live metadata")
		}
		previous = value.ID
	}
	return nil
}

func identifier(value string) bool {
	if value == "" || !bounded(value, MaxIDBytes) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}
func bounded(value string, limit int) bool { return utf8.ValidString(value) && len(value) <= limit }
func validateOptionalCount(value *int) error {
	if value != nil && (*value < 0 || *value > MaxCount) {
		return errors.New("numeric value outside range")
	}
	return nil
}

// FromParsed creates a deterministic family replacement and applies the wire
// allowlist. It intentionally drops paths, prompts, digest/tool text, commands,
// file edits, repository data, environment data, and synthesis.
func FromParsed(vendor, familyID, parserVersion, state, staleReason string, parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata, fingerprints []vendors.FileFingerprint, headerMappings ...[]HeaderMapping) (Family, error) {
	f := Family{SchemaVersion: SchemaVersion, ParserVersion: parserVersion, Vendor: vendor, FamilyID: familyID, State: state, StaleReason: truncate(staleReason, MaxDisplayBytes)}
	for _, p := range parsed {
		s := p.Session
		fact := Session{ID: s.ID, ParentID: p.ParentID, SpawnKey: p.SpawnKey, Name: truncate(p.Name, MaxDisplayBytes), Branch: optional(s.Branch, MaxDisplayBytes), Entrypoint: optional(s.Entrypoint, MaxDisplayBytes), StartedAtMs: s.StartedAt, LastActivityAtMs: s.LastActivityTime, DurationMs: cloneInt(s.DurationMs), Stopped: p.Stopped, InTurn: p.InTurn, Model: optional(s.Model, MaxModelBytes), ContextTokens: cloneInt(s.ContextTokens), ContextWindow: cloneInt(s.ContextWindow), Counts: Counts{EditedFiles: s.EditedFileCount, Turns: s.Turns, ToolUses: s.ToolUses, Errors: s.Errors, Compactions: s.Compactions, PullRequests: s.PullRequests}}
		if p.StatusHint != nil {
			fact.StatusHint = truncate(*p.StatusHint, MaxDisplayBytes)
		}
		if p.RecordedCost != nil {
			value := int64(math.Round(*p.RecordedCost * 1_000_000))
			fact.RecordedCostMicros = &value
		}
		models := make([]string, 0, len(s.Tokens))
		for model := range s.Tokens {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			t := s.Tokens[model]
			fact.Usage = append(fact.Usage, ModelUsage{Model: truncate(model, MaxModelBytes), InputTokens: t.InputTokens, OutputTokens: t.OutputTokens, CacheCreationInputTokens: t.CacheCreationInputTokens, CacheCreation1hInputTokens: t.CacheCreation1hInputTokens, CacheReadInputTokens: t.CacheReadInputTokens, CostMicroUSD: int64(math.Round(t.Cost * 1_000_000))})
		}
		spawnKeys := make([]string, 0, len(p.Spawns))
		for key := range p.Spawns {
			spawnKeys = append(spawnKeys, key)
		}
		sort.Strings(spawnKeys)
		for _, key := range spawnKeys {
			spawn := p.Spawns[key]
			fact.Spawns = append(fact.Spawns, Spawn{Key: key, Turn: cloneInt(spawn.Turn), Completed: spawn.Completed})
		}
		for _, command := range p.Commands {
			fact.CommandLabels = append(fact.CommandLabels, truncate(command.Label, MaxDisplayBytes))
		}
		f.Sessions = append(f.Sessions, fact)
	}
	sort.Slice(f.Sessions, func(i, j int) bool { return f.Sessions[i].ID < f.Sessions[j].ID })
	sessionIDs := make(map[string]bool, len(f.Sessions))
	for _, item := range f.Sessions {
		sessionIDs[item.ID] = true
	}
	if metadata != nil {
		for id, name := range metadata.Names {
			if !sessionIDs[id] {
				continue
			}
			f.Metadata.Names = append(f.Metadata.Names, MetadataName{ID: id, Name: truncate(name, MaxDisplayBytes)})
		}
		for id, status := range metadata.Live {
			if !sessionIDs[id] {
				continue
			}
			f.Metadata.Live = append(f.Metadata.Live, MetadataLive{ID: id, Status: truncate(status, MaxDisplayBytes)})
		}
		sort.Slice(f.Metadata.Names, func(i, j int) bool { return f.Metadata.Names[i].ID < f.Metadata.Names[j].ID })
		sort.Slice(f.Metadata.Live, func(i, j int) bool { return f.Metadata.Live[i].ID < f.Metadata.Live[j].ID })
	}
	for _, fp := range fingerprints {
		f.Fingerprints = append(f.Fingerprints, Fingerprint{Key: fp.Key, Size: fp.Size, ModifiedAtMs: fp.ModifiedAtMs})
	}
	sort.Slice(f.Fingerprints, func(i, j int) bool { return f.Fingerprints[i].Key < f.Fingerprints[j].Key })
	if len(headerMappings) > 0 {
		f.HeaderMappings = append([]HeaderMapping(nil), headerMappings[0]...)
		sort.Slice(f.HeaderMappings, func(i, j int) bool { return f.HeaderMappings[i].Key < f.HeaderMappings[j].Key })
	}
	return f, Validate(f)
}

func (f Family) Parsed() ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
	if err := Validate(f); err != nil {
		return nil, nil, err
	}
	parsed := make([]*vendors.ParsedSession, 0, len(f.Sessions))
	for _, fact := range f.Sessions {
		s := &session.Session{Agent: f.Vendor, ID: fact.ID, Branch: pointer(fact.Branch), DurationMs: cloneInt(fact.DurationMs), Tokens: map[string]session.ModelTokens{}, StartedAt: fact.StartedAtMs, LastActivityTime: fact.LastActivityAtMs, Entrypoint: pointer(fact.Entrypoint), EditedFileCount: fact.Counts.EditedFiles, SessionDetails: session.SessionDetails{Model: pointer(fact.Model), ContextTokens: cloneInt(fact.ContextTokens), ContextWindow: cloneInt(fact.ContextWindow), Turns: fact.Counts.Turns, ToolUses: fact.Counts.ToolUses, Errors: fact.Counts.Errors, Compactions: fact.Counts.Compactions, PullRequests: fact.Counts.PullRequests}}
		for _, usage := range fact.Usage {
			s.Tokens[usage.Model] = session.ModelTokens{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheCreationInputTokens: usage.CacheCreationInputTokens, CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens, CacheReadInputTokens: usage.CacheReadInputTokens, Cost: float64(usage.CostMicroUSD) / 1_000_000}
		}
		p := &vendors.ParsedSession{Session: s, ParentID: fact.ParentID, SpawnKey: fact.SpawnKey, Stopped: fact.Stopped, InTurn: fact.InTurn, Name: fact.Name, StatusHint: pointer(fact.StatusHint), Spawns: map[string]vendors.SpawnState{}}
		if fact.RecordedCostMicros != nil {
			value := float64(*fact.RecordedCostMicros) / 1_000_000
			p.RecordedCost = &value
		}
		for _, spawn := range fact.Spawns {
			p.Spawns[spawn.Key] = vendors.SpawnState{Turn: cloneInt(spawn.Turn), Completed: spawn.Completed}
		}
		for _, label := range fact.CommandLabels {
			p.Commands = append(p.Commands, session.SubagentCommand{Label: label})
		}
		parsed = append(parsed, p)
	}
	metadata := vendors.EmptySessionMetadata()
	for _, value := range f.Metadata.Names {
		metadata.Names[value.ID] = value.Name
	}
	for _, value := range f.Metadata.Live {
		metadata.Live[value.ID] = value.Status
	}
	return parsed, metadata, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
func optional(value *string, limit int) string {
	if value == nil {
		return ""
	}
	return truncate(*value, limit)
}
func pointer(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}
func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
