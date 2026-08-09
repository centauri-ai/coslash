package atlas

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The v1-to-v2 migration boundary.
//
// Schema v1 stored the pipeline as a record keyed by stage name; v2 stores a
// graph of agent seats and typed edges. Migration happens once, at the moment a
// document is decoded, and is idempotent: migrating an already-migrated board
// returns it unchanged, so a repeated import or a re-save can never compound.

// legacyBoard is the v1 record shape, decoded only to be rewritten.
type legacyBoard struct {
	Kind          string                     `json:"kind"`
	SchemaVersion uint64                     `json:"schemaVersion"`
	Instructions  string                     `json:"instructions"`
	SystemPrompts SystemPrompts              `json:"systemPrompts"`
	Components    map[string]json.RawMessage `json:"components"`
	Viewport      Viewport                   `json:"viewport"`

	extra map[string]json.RawMessage
}

// legacySeatComponent is one v1 stage that drives a model.
type legacySeatComponent struct {
	Prompt      string       `json:"prompt"`
	PromptDraft string       `json:"promptDraft"`
	Seat        Seat         `json:"seat"`
	Seats       []WorkerSeat `json:"seats"`
	Committee   Committee    `json:"committee"`
	Box         NodeBox      `json:"box"`
	PromptBox   NodeBox      `json:"promptBox"`
	InfoBox     NodeBox      `json:"infoBox"`

	extra map[string]json.RawMessage
}

type legacyVerifyComponent struct {
	Checks []Check `json:"checks"`
}

type legacyPublishComponent struct {
	Publish PublishConfig `json:"publish"`
}

// DecodeBoard parses an Atlas board document of any supported schema version,
// migrates it to the current one, and normalizes it.
//
// It refuses rather than repairs in exactly two cases: a document that is not
// an Atlas board, and a schema version this build is too old to understand.
// Everything else is a repairable board — Normalize is total, so a partially
// corrupt document yields a coherent graph rather than an error the user cannot
// act on.
func DecodeBoard(raw []byte) (*Board, error) {
	var probe struct {
		Kind          string           `json:"kind"`
		SchemaVersion *uint64          `json:"schemaVersion"`
		Version       *json.RawMessage `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, newError(CodeCorruptDocument, "the board document is not valid JSON").
			withDetail(err.Error()).withCause(err)
	}
	if probe.Kind != "" && probe.Kind != BoardKind {
		return nil, policyError("kind", "the document is not an Atlas board")
	}
	// A Columbus board carries `version` instead of `schemaVersion`. Accepting
	// it would normalize another canvas's document into an Atlas board and
	// destroy it on the next save.
	if probe.SchemaVersion == nil {
		if probe.Version != nil {
			return nil, policyError("schemaVersion", "the document is not an Atlas board")
		}
		return nil, policyError("schemaVersion", "the board is missing its schema version")
	}

	switch *probe.SchemaVersion {
	case LegacyBoardSchemaVersion:
		board, err := migrateLegacyBoard(raw)
		if err != nil {
			return nil, err
		}
		Normalize(board)
		return board, nil
	case BoardSchemaVersion:
		var board Board
		if err := json.Unmarshal(raw, &board); err != nil {
			return nil, newError(CodeCorruptDocument, "the board graph could not be decoded").
				withDetail(err.Error()).withCause(err)
		}
		Normalize(&board)
		return &board, nil
	default:
		// A future major version may carry members whose meaning this build
		// cannot guess. Refusing loudly is the only option that cannot silently
		// discard configuration.
		return nil, (&Error{
			Code:    CodeSchemaVersion,
			Message: fmt.Sprintf("this build supports Atlas board schema %d; the document declares %d", BoardSchemaVersion, *probe.SchemaVersion),
			Field:   "schemaVersion",
		})
	}
}

// migrateLegacyBoard rewrites a v1 record board as a v2 graph.
//
// The three seated stages become seats on a plan → build → review chain with
// the starter edges. Verify checks and the publish target have no card in the
// graph, so they move to RunPolicy rather than being dropped — a run that
// silently lost its configured checks would verify nothing and still succeed.
func migrateLegacyBoard(raw []byte) (*Board, error) {
	var legacy legacyBoard
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, newError(CodeCorruptDocument, "the legacy board could not be decoded").
			withDetail(err.Error()).withCause(err)
	}
	legacy.extra = captureExtra(raw, legacyBoard{})

	roles := []ComponentID{ComponentPlan, ComponentBuild, ComponentReview}
	components := make([]AgentComponent, 0, len(roles))
	for index, role := range roles {
		stage := role
		component := NewAgentSeat(string(role), &stage, index, 1)
		encoded, ok := legacy.Components[string(role)]
		if ok && len(encoded) > 0 {
			var saved legacySeatComponent
			if err := json.Unmarshal(encoded, &saved); err == nil {
				saved.extra = captureExtra(encoded, legacySeatComponent{})
				applyLegacySeat(&component, saved, &stage)
			}
		}
		components = append(components, component)
	}

	board := &Board{
		Kind:          BoardKind,
		SchemaVersion: BoardSchemaVersion,
		Instructions:  legacy.Instructions,
		SystemPrompts: legacy.SystemPrompts,
		Components:    components,
		Edges: []Edge{
			{ID: "edge-plan-build", From: "plan", To: "build", Kind: EdgeTrigger, Mode: TriggerAuto},
			{ID: "edge-build-review", From: "build", To: "review", Kind: EdgeTrigger, Mode: TriggerAuto},
			{
				ID: "edge-review-build", From: "review", To: "build",
				Kind: EdgeFeedback, Mode: TriggerAuto, MaxRounds: DefaultFeedbackMaxRounds,
			},
		},
		RunPolicy: migrateLegacyRunPolicy(legacy.Components),
		Viewport:  legacy.Viewport,
		extra:     cloneExtra(legacy.extra),
	}
	return board, nil
}

func applyLegacySeat(component *AgentComponent, saved legacySeatComponent, role *ComponentID) {
	prompt := saved.Prompt
	if strings.TrimSpace(prompt) == "" {
		// Older copies stored the seat's steering under promptDraft.
		prompt = saved.PromptDraft
	}
	component.Prompt = prompt
	component.Seat = saved.Seat
	component.Seats = saved.Seats
	if strings.TrimSpace(saved.Committee.ConsolidationPrompt) != "" {
		component.Committee = saved.Committee
	}
	// A v1 card has no companions and must grow exactly once, so the saved box
	// is handed to normalization rather than merged with the synthesized
	// default that NewAgentSeat already produced.
	if !isZeroBox(saved.Box) {
		component.Box = saved.Box
	}
	component.PromptBox = saved.PromptBox
	component.InfoBox = saved.InfoBox
	component.RequiredOutputs = DefaultRequiredOutputs(role)
	component.extra = cloneExtra(saved.extra)
}

func migrateLegacyRunPolicy(components map[string]json.RawMessage) *RunPolicy {
	policy := RunPolicy{}
	carried := false
	if encoded, ok := components[string(ComponentVerify)]; ok && len(encoded) > 0 {
		var verify legacyVerifyComponent
		if err := json.Unmarshal(encoded, &verify); err == nil && len(verify.Checks) > 0 {
			policy.Checks = verify.Checks
			carried = true
		}
	}
	if encoded, ok := components[string(ComponentPublish)]; ok && len(encoded) > 0 {
		var publish legacyPublishComponent
		if err := json.Unmarshal(encoded, &publish); err == nil {
			policy.Publish = publish.Publish
			if publish.Publish.Base != "" || publish.Publish.Draft {
				carried = true
			}
		}
	}
	if !carried {
		return nil
	}
	return &policy
}
