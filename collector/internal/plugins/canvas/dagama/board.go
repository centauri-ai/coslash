package dagama

import (
	"encoding/json"
	"time"
)

// BoardSchemaVersion is the current durable board schema.
const BoardSchemaVersion uint64 = 1

// Steering bounds. A prompt card shapes how a stage does its work; it is not a
// place to paste a corpus, and an unbounded one would push the contract and the
// evidence out of the assembled prompt's size budget.
const (
	MaxInstructionsChars = 8_000
	MaxPromptChars       = 8_000
)

// Board is one DaGama pipeline configuration.
//
// A board is a JSON file in a project directory. It can be hand-edited,
// committed, shared, or arrive in a pull request, so it is untrusted input:
// Normalize repairs, Policy refuses, and the in-memory model stays strict.
type Board struct {
	SchemaVersion uint64 `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	ProjectID     string `json:"projectId"`
	ProjectPath   string `json:"projectPath"`
	// Instructions are the project conventions applied to every seat in every
	// run. Operator-authored steering: it can change how work is done, never
	// what counts as done.
	Instructions string     `json:"instructions"`
	Revision     uint64     `json:"revision"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	Components   Components `json:"components"`

	// extra preserves fields written by a newer coSlash so an older one does not
	// silently drop them on round trip. Without this, opening a board in an older
	// build and saving it would delete configuration the user never saw.
	extra map[string]json.RawMessage
}

// Components is the fixed pipeline. The set is closed: a board cannot introduce
// a stage, because the shape is the product.
type Components struct {
	Intake  IntakeComponent  `json:"intake"`
	Plan    SeatComponent    `json:"plan"`
	Build   SeatComponent    `json:"build"`
	Verify  VerifyComponent  `json:"verify"`
	Review  SeatComponent    `json:"review"`
	Publish PublishComponent `json:"publish"`

	extra map[string]json.RawMessage
}

// Seat is the model configuration for a component that runs an agent.
type Seat struct {
	Vendor     Vendor `json:"vendor"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
	Permission string `json:"permission"`

	extra map[string]json.RawMessage
}

// IntakeComponent renders a template; it runs no model.
type IntakeComponent struct {
	Template string `json:"template,omitempty"`

	extra map[string]json.RawMessage
}

// SeatComponent is a component that drives an agent CLI.
type SeatComponent struct {
	Seat Seat `json:"seat"`
	// Prompt is this stage's operator steering, carried into the assembled
	// prompt beside the project instructions.
	Prompt string `json:"prompt"`

	extra map[string]json.RawMessage
}

// VerifyComponent runs argv-only checks.
type VerifyComponent struct {
	Checks []Check `json:"checks"`

	extra map[string]json.RawMessage
}

// Check is one configured verification command.
type Check struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`

	extra map[string]json.RawMessage
}

// PublishComponent drives git and gh.
type PublishComponent struct {
	Publish PublishConfig `json:"publish"`

	extra map[string]json.RawMessage
}

// PublishConfig selects the pull request target and draft state.
type PublishConfig struct {
	// Base is empty for "the selected project's checked-out branch", resolved at
	// preflight — not origin/HEAD or main by default. A linked worktree is
	// usually on a feature branch whose tip would otherwise be discarded.
	Base string `json:"base"`
	// Draft defaults to true; a board must opt out explicitly.
	Draft bool `json:"draft"`

	extra map[string]json.RawMessage
}

// ---------------------------------------------------------------------------
// Round-trip preservation
//
// Each type below decodes its known fields normally, keeps whatever else the
// document carried, and re-emits both on encode. json.Marshal orders map keys,
// so the encoding stays byte-deterministic and golden tests remain stable.
// ---------------------------------------------------------------------------

func decodeWithExtra(data []byte, known any, knownFields []string) (map[string]json.RawMessage, error) {
	if err := json.Unmarshal(data, known); err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	for _, field := range knownFields {
		delete(all, field)
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

func encodeWithExtra(known any, extra map[string]json.RawMessage) ([]byte, error) {
	encoded, err := json.Marshal(known)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return encoded, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &merged); err != nil {
		return nil, err
	}
	for name, value := range extra {
		if _, taken := merged[name]; !taken {
			merged[name] = value
		}
	}
	return json.Marshal(merged)
}

var (
	boardFields = []string{
		"schemaVersion", "id", "name", "projectId", "projectPath", "instructions",
		"revision", "createdAt", "updatedAt", "components",
	}
	componentsFields = []string{"intake", "plan", "build", "verify", "review", "publish"}
	seatFields       = []string{"vendor", "model", "effort", "permission"}
	intakeFields     = []string{"template"}
	seatCompFields   = []string{"seat", "prompt"}
	verifyFields     = []string{"checks"}
	checkFields      = []string{"name", "argv"}
	publishCompField = []string{"publish"}
	publishFields    = []string{"base", "draft"}
)

func (b *Board) UnmarshalJSON(data []byte) error {
	type alias Board
	var known alias
	extra, err := decodeWithExtra(data, &known, boardFields)
	if err != nil {
		return err
	}
	*b = Board(known)
	b.extra = extra
	return nil
}

// MarshalJSON encodes the board with any preserved unknown fields.
func (b Board) MarshalJSON() ([]byte, error) {
	type alias Board
	return encodeWithExtra(alias(b), b.extra)
}

func (c *Components) UnmarshalJSON(data []byte) error {
	type alias Components
	var known alias
	extra, err := decodeWithExtra(data, &known, componentsFields)
	if err != nil {
		return err
	}
	*c = Components(known)
	c.extra = extra
	return nil
}

// MarshalJSON encodes the pipeline with any preserved unknown fields.
func (c Components) MarshalJSON() ([]byte, error) {
	type alias Components
	return encodeWithExtra(alias(c), c.extra)
}

func (s *Seat) UnmarshalJSON(data []byte) error {
	type alias Seat
	var known alias
	extra, err := decodeWithExtra(data, &known, seatFields)
	if err != nil {
		return err
	}
	*s = Seat(known)
	s.extra = extra
	return nil
}

// MarshalJSON encodes the seat with any preserved unknown fields.
func (s Seat) MarshalJSON() ([]byte, error) {
	type alias Seat
	return encodeWithExtra(alias(s), s.extra)
}

func (c *IntakeComponent) UnmarshalJSON(data []byte) error {
	type alias IntakeComponent
	var known alias
	extra, err := decodeWithExtra(data, &known, intakeFields)
	if err != nil {
		return err
	}
	*c = IntakeComponent(known)
	c.extra = extra
	return nil
}

// MarshalJSON encodes the intake component with any preserved unknown fields.
func (c IntakeComponent) MarshalJSON() ([]byte, error) {
	type alias IntakeComponent
	return encodeWithExtra(alias(c), c.extra)
}

func (c *SeatComponent) UnmarshalJSON(data []byte) error {
	type alias SeatComponent
	var known alias
	extra, err := decodeWithExtra(data, &known, seatCompFields)
	if err != nil {
		return err
	}
	*c = SeatComponent(known)
	c.extra = extra
	return nil
}

// MarshalJSON encodes the seat component with any preserved unknown fields.
func (c SeatComponent) MarshalJSON() ([]byte, error) {
	type alias SeatComponent
	return encodeWithExtra(alias(c), c.extra)
}

func (c *VerifyComponent) UnmarshalJSON(data []byte) error {
	type alias VerifyComponent
	var known alias
	extra, err := decodeWithExtra(data, &known, verifyFields)
	if err != nil {
		return err
	}
	*c = VerifyComponent(known)
	c.extra = extra
	return nil
}

// MarshalJSON encodes the verify component with any preserved unknown fields.
func (c VerifyComponent) MarshalJSON() ([]byte, error) {
	type alias VerifyComponent
	return encodeWithExtra(alias(c), c.extra)
}

func (c *Check) UnmarshalJSON(data []byte) error {
	type alias Check
	var known alias
	extra, err := decodeWithExtra(data, &known, checkFields)
	if err != nil {
		return err
	}
	*c = Check(known)
	c.extra = extra
	return nil
}

// MarshalJSON encodes the check with any preserved unknown fields.
func (c Check) MarshalJSON() ([]byte, error) {
	type alias Check
	return encodeWithExtra(alias(c), c.extra)
}

func (c *PublishComponent) UnmarshalJSON(data []byte) error {
	type alias PublishComponent
	var known alias
	extra, err := decodeWithExtra(data, &known, publishCompField)
	if err != nil {
		return err
	}
	*c = PublishComponent(known)
	c.extra = extra
	return nil
}

// MarshalJSON encodes the publish component with any preserved unknown fields.
func (c PublishComponent) MarshalJSON() ([]byte, error) {
	type alias PublishComponent
	return encodeWithExtra(alias(c), c.extra)
}

func (c *PublishConfig) UnmarshalJSON(data []byte) error {
	type alias PublishConfig
	// Draft defaults to true, so a document that omits it must not decode as
	// false. Seed the default before decoding.
	known := alias{Draft: true}
	extra, err := decodeWithExtra(data, &known, publishFields)
	if err != nil {
		return err
	}
	*c = PublishConfig(known)
	c.extra = extra
	return nil
}

// MarshalJSON encodes the publish config with any preserved unknown fields.
func (c PublishConfig) MarshalJSON() ([]byte, error) {
	type alias PublishConfig
	return encodeWithExtra(alias(c), c.extra)
}

// UnknownFields exposes the preserved top-level fields for diagnostics and tests.
func (b Board) UnknownFields() map[string]json.RawMessage { return b.extra }
