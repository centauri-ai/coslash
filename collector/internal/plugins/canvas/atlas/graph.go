package atlas

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// The Atlas board: a user-composed graph of agent seats connected by typed
// edges, plus the shared context, role prompts, and run policy a run reads.
//
// The board stores membership and order as data (components + edges). The run
// controller still executes the classic Intake → … → Publish pipeline when the
// board is a legacyRole plan → build → review chain; freeform graphs are
// editable but not runnable yet.
//
// This file is deliberately pure: no filesystem, no clock, no process. Normalize
// repairs an untrusted document into a coherent one and never fails; AssertPolicy
// refuses one whose executable content is not allowed and never repairs. Both
// run again server-side before anything is executed.

// BoardKind distinguishes an Atlas board from another canvas's document.
const BoardKind = "atlas"

// Board schema versions. The graph is versioned independently of the storage
// envelope that carries it, because a board can be exported and re-imported
// without its document metadata.
const (
	// BoardSchemaVersion is the current graph schema.
	BoardSchemaVersion uint64 = 2
	// LegacyBoardSchemaVersion is the record-shaped schema accepted at the
	// migration boundary and rewritten to the current one.
	LegacyBoardSchemaVersion uint64 = 1
)

// Canvas world bounds and default geometry. These are the visual reference the
// migration must preserve, so they are stated once and read from here.
const (
	WorldWidth  = 5200.0
	WorldHeight = 3200.0
	MinZoom     = 0.25
	MaxZoom     = 2.0
	DefaultZoom = 0.55

	NodeMinWidth  = 240.0
	NodeMinHeight = 120.0

	seatTerminalWidth  = 440.0
	seatTerminalHeight = 760.0
	seatPromptWidth    = 380.0
	seatPromptHeight   = 260.0
	seatInfoWidth      = 380.0
	seatInfoHeight     = 320.0
	companionGap       = 20.0
	clusterStackGap    = 28.0
	stageGap           = 90.0
	railX              = 120.0
	railY              = 160.0
)

// Feedback rounds bound automatic repair Builds after the first instance.
// Exhaustion opens a human gate rather than looping.
const (
	DefaultFeedbackMaxRounds uint64 = 1
	MaxFeedbackRounds        uint64 = 2
)

// WorkerRole distinguishes the seat that writes the promoted artifact from the
// siblings that only draft.
type WorkerRole string

const (
	// RoleWorker drafts. A sole worker is always a worker, never a main.
	RoleWorker WorkerRole = "worker"
	// RoleMain refines sibling drafts into the promoted artifact when N > 1.
	RoleMain WorkerRole = "main"
)

// EdgeKind separates forward advancement from repair.
type EdgeKind string

const (
	// EdgeTrigger advances the next seat once the upstream seat succeeds.
	EdgeTrigger EdgeKind = "trigger"
	// EdgeFeedback restarts Build after a failed verification or review.
	EdgeFeedback EdgeKind = "feedback"
)

// TriggerMode decides whether an edge advances on its own.
type TriggerMode string

const (
	// TriggerAuto advances immediately.
	TriggerAuto TriggerMode = "auto"
	// TriggerManual waits for an explicit Go on the edge.
	TriggerManual TriggerMode = "manual"
)

// NodeBox is one canvas card's placement and display state.
type NodeBox struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Width     float64 `json:"width"`
	Height    float64 `json:"height"`
	Collapsed bool    `json:"collapsed"`
	Locked    bool    `json:"locked"`

	extra map[string]json.RawMessage
}

// Viewport is the saved camera.
type Viewport struct {
	Zoom float64 `json:"zoom"`
	PanX float64 `json:"panX"`
	PanY float64 `json:"panY"`

	extra map[string]json.RawMessage
}

// Seat is the model configuration a component drives.
type Seat struct {
	Vendor     Vendor `json:"vendor"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
	Permission string `json:"permission"`

	extra map[string]json.RawMessage
}

// WorkerSeat is one committee member. The profile fields are inline rather than
// nested, matching the saved legacy shape.
type WorkerSeat struct {
	ID         string     `json:"id"`
	Role       WorkerRole `json:"role"`
	Vendor     Vendor     `json:"vendor"`
	Model      string     `json:"model"`
	Effort     string     `json:"effort"`
	Permission string     `json:"permission"`

	extra map[string]json.RawMessage
}

// Profile returns the worker's model configuration.
func (w WorkerSeat) Profile() Seat {
	return Seat{Vendor: w.Vendor, Model: w.Model, Effort: w.Effort, Permission: w.Permission}
}

func (w *WorkerSeat) applyProfile(seat Seat) {
	w.Vendor = seat.Vendor
	w.Model = seat.Model
	w.Effort = seat.Effort
	w.Permission = seat.Permission
}

// Committee holds the refine rules the main worker follows when N > 1. The
// member name is kept for boards already on disk.
type Committee struct {
	ConsolidationPrompt string `json:"consolidationPrompt"`

	extra map[string]json.RawMessage
}

// Check is one configured verification command.
type Check struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`

	extra map[string]json.RawMessage
}

// PublishConfig is the pull-request target for a successful run.
type PublishConfig struct {
	Base  string `json:"base"`
	Draft bool   `json:"draft"`

	extra map[string]json.RawMessage
}

// RunPolicy is the executable configuration a run reads that is not attached to
// one seat.
//
// Schema v1 kept these under the `verify` and `publish` pipeline records. The
// graph has no card for either, so they move here at the migration boundary
// rather than being dropped: a run that silently lost its configured checks
// would verify nothing and still report success.
type RunPolicy struct {
	Checks  []Check       `json:"checks"`
	Publish PublishConfig `json:"publish"`

	extra map[string]json.RawMessage
}

// AgentComponent is one agent seat on the canvas: prompt, model or committee,
// required outputs, and the three cards that render it.
type AgentComponent struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
	// Seat mirrors the sole worker, or the main worker when N > 1.
	Seat            Seat         `json:"seat"`
	Seats           []WorkerSeat `json:"seats"`
	Committee       Committee    `json:"committee"`
	RequiredOutputs []string     `json:"requiredOutputs"`
	Box             NodeBox      `json:"box"`
	PromptBox       NodeBox      `json:"promptBox"`
	InfoBox         NodeBox      `json:"infoBox"`
	// LegacyRole binds a seat to a stage of the runnable pipeline. Nil for a
	// freeform seat, which is editable but not yet runnable.
	LegacyRole *ComponentID `json:"legacyRole"`

	extra map[string]json.RawMessage
}

// Edge connects two seats. A trigger advances; a feedback repairs.
type Edge struct {
	ID   string      `json:"id"`
	From string      `json:"from"`
	To   string      `json:"to"`
	Kind EdgeKind    `json:"kind"`
	Mode TriggerMode `json:"mode"`
	// MaxRounds caps automatic repair Builds after instance 1 and is only
	// meaningful on a feedback edge.
	MaxRounds uint64 `json:"maxRounds,omitempty"`

	extra map[string]json.RawMessage
}

// Board is one Atlas graph.
type Board struct {
	Kind          string           `json:"kind"`
	SchemaVersion uint64           `json:"schemaVersion"`
	Instructions  string           `json:"instructions"`
	SystemPrompts SystemPrompts    `json:"systemPrompts"`
	Components    []AgentComponent `json:"components"`
	Edges         []Edge           `json:"edges"`
	// RunPolicy is omitted from a board that configures neither checks nor a
	// publish target, so a default board round-trips to the shape the frozen
	// reference fixture records.
	RunPolicy *RunPolicy `json:"runPolicy,omitempty"`
	Viewport  Viewport   `json:"viewport"`

	extra map[string]json.RawMessage
}

// ---------------------------------------------------------------------------
// Round-trip preservation
// ---------------------------------------------------------------------------

func (b *Board) UnmarshalJSON(data []byte) error {
	type alias Board
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*b = Board(shadow)
	b.extra = captureExtra(data, Board{})
	return nil
}

func (b Board) MarshalJSON() ([]byte, error) {
	type alias Board
	encoded, err := json.Marshal(alias(b))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, b.extra)
}

func (c *AgentComponent) UnmarshalJSON(data []byte) error {
	type alias AgentComponent
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*c = AgentComponent(shadow)
	c.extra = captureExtra(data, AgentComponent{})
	return nil
}

func (c AgentComponent) MarshalJSON() ([]byte, error) {
	type alias AgentComponent
	encoded, err := json.Marshal(alias(c))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, c.extra)
}

func (s *Seat) UnmarshalJSON(data []byte) error {
	type alias Seat
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*s = Seat(shadow)
	s.extra = captureExtra(data, Seat{})
	return nil
}

func (s Seat) MarshalJSON() ([]byte, error) {
	type alias Seat
	encoded, err := json.Marshal(alias(s))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, s.extra)
}

func (w *WorkerSeat) UnmarshalJSON(data []byte) error {
	type alias WorkerSeat
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*w = WorkerSeat(shadow)
	w.extra = captureExtra(data, WorkerSeat{})
	return nil
}

func (w WorkerSeat) MarshalJSON() ([]byte, error) {
	type alias WorkerSeat
	encoded, err := json.Marshal(alias(w))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, w.extra)
}

func (c *Committee) UnmarshalJSON(data []byte) error {
	type alias Committee
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*c = Committee(shadow)
	c.extra = captureExtra(data, Committee{})
	return nil
}

func (c Committee) MarshalJSON() ([]byte, error) {
	type alias Committee
	encoded, err := json.Marshal(alias(c))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, c.extra)
}

func (e *Edge) UnmarshalJSON(data []byte) error {
	type alias Edge
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*e = Edge(shadow)
	e.extra = captureExtra(data, Edge{})
	return nil
}

func (e Edge) MarshalJSON() ([]byte, error) {
	type alias Edge
	encoded, err := json.Marshal(alias(e))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, e.extra)
}

func (b *NodeBox) UnmarshalJSON(data []byte) error {
	type alias NodeBox
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*b = NodeBox(shadow)
	b.extra = captureExtra(data, NodeBox{})
	return nil
}

func (b NodeBox) MarshalJSON() ([]byte, error) {
	type alias NodeBox
	encoded, err := json.Marshal(alias(b))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, b.extra)
}

func (v *Viewport) UnmarshalJSON(data []byte) error {
	type alias Viewport
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*v = Viewport(shadow)
	v.extra = captureExtra(data, Viewport{})
	return nil
}

func (v Viewport) MarshalJSON() ([]byte, error) {
	type alias Viewport
	encoded, err := json.Marshal(alias(v))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, v.extra)
}

func (c *Check) UnmarshalJSON(data []byte) error {
	type alias Check
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*c = Check(shadow)
	c.extra = captureExtra(data, Check{})
	return nil
}

func (c Check) MarshalJSON() ([]byte, error) {
	type alias Check
	encoded, err := json.Marshal(alias(c))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, c.extra)
}

func (p *PublishConfig) UnmarshalJSON(data []byte) error {
	type alias PublishConfig
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*p = PublishConfig(shadow)
	p.extra = captureExtra(data, PublishConfig{})
	return nil
}

func (p PublishConfig) MarshalJSON() ([]byte, error) {
	type alias PublishConfig
	encoded, err := json.Marshal(alias(p))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, p.extra)
}

func (p *RunPolicy) UnmarshalJSON(data []byte) error {
	type alias RunPolicy
	var shadow alias
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	*p = RunPolicy(shadow)
	p.extra = captureExtra(data, RunPolicy{})
	return nil
}

func (p RunPolicy) MarshalJSON() ([]byte, error) {
	type alias RunPolicy
	encoded, err := json.Marshal(alias(p))
	if err != nil {
		return nil, err
	}
	return mergeExtra(encoded, p.extra)
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

var legacyDefaultOutputs = map[ComponentID][]string{
	ComponentPlan:   {"PLAN.md"},
	ComponentBuild:  {"IMPLEMENTATION.md", "CHANGESET.patch", "change.json"},
	ComponentReview: {"REVIEW.md", "review.json"},
}

var legacyTitles = map[ComponentID]string{
	ComponentPlan:   "PLAN",
	ComponentBuild:  "BUILD",
	ComponentReview: "REVIEW",
}

var legacyConsolidationPrompts = map[ComponentID]string{
	ComponentPlan:   "Read every sibling draft. Challenge weak assumptions, resolve conflicts, and refine your own plan into one coherent final PLAN.md.",
	ComponentBuild:  "Read sibling notes. Prefer the strongest correct approach and refine your own change.",
	ComponentReview: "Reconcile sibling reviews. Prefer fail-closed findings; refine your verdict into one final review.",
}

const freeformConsolidationPrompt = "Read sibling drafts and refine your own result into one coherent output."

// DefaultSeatForVendor returns the verified starting profile for a vendor.
func DefaultSeatForVendor(vendor Vendor) Seat {
	if vendor == VendorCodex {
		return Seat{Vendor: VendorCodex, Model: "gpt-5.6-terra", Effort: "high", Permission: "workspace-write"}
	}
	return Seat{Vendor: VendorClaude, Model: "opus", Effort: "high", Permission: "acceptEdits"}
}

// DefaultSeatForRole returns the profile a role starts with. Review defaults to
// the other vendor so a review is not written by the model that built.
func DefaultSeatForRole(role *ComponentID) Seat {
	if role != nil && *role == ComponentReview {
		return DefaultSeatForVendor(VendorCodex)
	}
	return DefaultSeatForVendor(VendorClaude)
}

// DefaultTerminalBox returns the seat card placement for a rail position.
func DefaultTerminalBox(index int) NodeBox {
	return NodeBox{
		X:      railX + float64(index)*(seatTerminalWidth+stageGap),
		Y:      railY,
		Width:  seatTerminalWidth,
		Height: seatTerminalHeight,
	}
}

// DefaultPromptBox returns the prompt card placement beneath a seat card.
func DefaultPromptBox(terminal NodeBox) NodeBox {
	return NodeBox{
		X:      terminal.X,
		Y:      terminal.Y + terminal.Height + clusterStackGap,
		Width:  seatPromptWidth,
		Height: seatPromptHeight,
	}
}

// DefaultInfoBox returns the info card placement beside the prompt card.
func DefaultInfoBox(terminal NodeBox) NodeBox {
	return NodeBox{
		X:      terminal.X + seatPromptWidth + companionGap,
		Y:      terminal.Y + terminal.Height + clusterStackGap,
		Width:  seatInfoWidth,
		Height: seatInfoHeight,
	}
}

// DefaultRequiredOutputs returns the artifacts a role must produce.
func DefaultRequiredOutputs(role *ComponentID) []string {
	if role != nil {
		if outputs, ok := legacyDefaultOutputs[*role]; ok {
			return append([]string(nil), outputs...)
		}
	}
	return []string{"OUTPUT.md"}
}

// DefaultCommittee returns the refine rules a role starts with.
func DefaultCommittee(role *ComponentID) Committee {
	if role != nil {
		if prompt, ok := legacyConsolidationPrompts[*role]; ok {
			return Committee{ConsolidationPrompt: prompt}
		}
	}
	return Committee{ConsolidationPrompt: freeformConsolidationPrompt}
}

// WorkerSeatID is the deterministic identifier for a committee member.
//
// The legacy client minted a UUID here. Deriving the identifier from the
// component and position instead keeps Normalize pure, which is what makes
// migration idempotent and golden replay reproducible.
func WorkerSeatID(componentID string, index int) string {
	return fmt.Sprintf("%s-worker-%d", componentID, index+1)
}

// NewAgentSeat builds a seat with role defaults applied.
func NewAgentSeat(id string, role *ComponentID, index int, workerCount int) AgentComponent {
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > MaxCommitteeWorkers {
		workerCount = MaxCommitteeWorkers
	}
	if id == "" {
		if role != nil {
			id = string(*role)
		} else {
			id = fmt.Sprintf("seat-%d", index+1)
		}
	}
	profile := DefaultSeatForRole(role)
	seats := make([]WorkerSeat, 0, workerCount)
	for worker := range workerCount {
		seat := WorkerSeat{ID: WorkerSeatID(id, worker), Role: RoleWorker}
		seat.applyProfile(profile)
		seats = append(seats, seat)
	}
	applyCommitteeRoles(seats)

	title := fmt.Sprintf("Seat %d", index+1)
	if role != nil {
		if legacy, ok := legacyTitles[*role]; ok {
			title = legacy
		}
	}
	box := DefaultTerminalBox(index)
	return AgentComponent{
		ID:              id,
		Title:           title,
		Seat:            primaryWorker(seats).Profile(),
		Seats:           seats,
		Committee:       DefaultCommittee(role),
		RequiredOutputs: DefaultRequiredOutputs(role),
		Box:             box,
		PromptBox:       DefaultPromptBox(box),
		InfoBox:         DefaultInfoBox(box),
		LegacyRole:      role,
	}
}

// DefaultBoard returns the starter plan → build → review chain.
func DefaultBoard() *Board {
	plan, build, review := ComponentPlan, ComponentBuild, ComponentReview
	board := &Board{
		Kind:          BoardKind,
		SchemaVersion: BoardSchemaVersion,
		SystemPrompts: DefaultSystemPrompts(),
		Components: []AgentComponent{
			NewAgentSeat("plan", &plan, 0, 1),
			NewAgentSeat("build", &build, 1, 1),
			NewAgentSeat("review", &review, 2, 1),
		},
		Edges: []Edge{
			{ID: "edge-plan-build", From: "plan", To: "build", Kind: EdgeTrigger, Mode: TriggerAuto},
			{ID: "edge-build-review", From: "build", To: "review", Kind: EdgeTrigger, Mode: TriggerAuto},
			{
				ID: "edge-review-build", From: "review", To: "build",
				Kind: EdgeFeedback, Mode: TriggerAuto, MaxRounds: DefaultFeedbackMaxRounds,
			},
		},
		Viewport: Viewport{Zoom: DefaultZoom},
	}
	return board
}

// ---------------------------------------------------------------------------
// Normalization — repair, never refuse
// ---------------------------------------------------------------------------

// Normalize repairs a decoded board in place.
//
// It is total and deterministic: every input produces a coherent board, the
// same input always produces the same board, and normalizing an already
// normalized board changes nothing. Those three properties are what let the
// migration boundary be idempotent and the golden tests be reproducible.
func Normalize(board *Board) {
	if board == nil {
		return
	}
	board.Kind = BoardKind
	board.SchemaVersion = BoardSchemaVersion
	board.Instructions = clampText(board.Instructions, MaxInstructionsBytes)
	normalizeSystemPrompts(&board.SystemPrompts)

	if len(board.Components) > MaxComponents {
		board.Components = board.Components[:MaxComponents]
	}
	components := make([]AgentComponent, 0, len(board.Components))
	seen := make(map[string]struct{}, len(board.Components))
	for _, component := range board.Components {
		normalized, ok := normalizeComponent(component, len(components))
		if !ok {
			continue
		}
		if _, duplicate := seen[normalized.ID]; duplicate {
			continue
		}
		seen[normalized.ID] = struct{}{}
		components = append(components, normalized)
	}
	if len(components) == 0 {
		defaults := DefaultBoard()
		components = defaults.Components
		for _, component := range components {
			seen[component.ID] = struct{}{}
		}
		if len(board.Edges) == 0 {
			board.Edges = defaults.Edges
		}
	}
	board.Components = components
	board.Edges = ensureLegacyFeedbackEdge(components, normalizeEdges(board.Edges, seen))
	normalizeRunPolicy(board)
	normalizeViewport(&board.Viewport)
}

func normalizeComponent(component AgentComponent, index int) (AgentComponent, bool) {
	role := normalizeLegacyRole(component.LegacyRole, component.ID)
	id := strings.TrimSpace(component.ID)
	if len(id) > MaxIDLength {
		id = id[:MaxIDLength]
	}
	if !ValidGraphID(id) {
		if role == nil {
			return AgentComponent{}, false
		}
		id = string(*role)
	}
	// A companion card's node id is derived by suffix, so a component that
	// already ends in one would make two different nodes collide.
	if strings.HasSuffix(id, "-prompt") || strings.HasSuffix(id, "-info") {
		return AgentComponent{}, false
	}

	fallbackBox := DefaultTerminalBox(index)
	box := migrateSeatTerminalBox(component, fallbackBox)
	seat := normalizeSeat(component.Seat, role)
	seats := normalizeSeats(component.Seats, role, seat, id)
	title := strings.TrimSpace(component.Title)
	if title == "" {
		title = fmt.Sprintf("Seat %d", index+1)
		if role != nil {
			if legacy, ok := legacyTitles[*role]; ok {
				title = legacy
			}
		}
	}

	return AgentComponent{
		ID:              id,
		Title:           clampText(title, MaxTitleLength),
		Prompt:          clampText(component.Prompt, MaxPromptLength),
		Seat:            seat,
		Seats:           seats,
		Committee:       normalizeCommittee(component.Committee, role),
		RequiredOutputs: normalizeRequiredOutputs(component.RequiredOutputs, role),
		Box:             box,
		PromptBox:       normalizeBox(component.PromptBox, DefaultPromptBox(box)),
		InfoBox:         normalizeBox(component.InfoBox, DefaultInfoBox(box)),
		LegacyRole:      role,
		extra:           cloneExtra(component.extra),
	}, true
}

// normalizeLegacyRole accepts the declared role, falling back to a component id
// that names a run stage — the shape pre-legacyRole boards were saved in.
func normalizeLegacyRole(declared *ComponentID, id string) *ComponentID {
	if declared != nil && ValidLegacyRole(*declared) {
		role := *declared
		return &role
	}
	if declared != nil {
		// A declared but unknown role is dropped rather than reinterpreted: the
		// component id may coincidentally name a stage it was never bound to.
		return nil
	}
	candidate := ComponentID(id)
	if ValidLegacyRole(candidate) {
		return &candidate
	}
	return nil
}

func normalizeSeat(seat Seat, role *ComponentID) Seat {
	fallback := DefaultSeatForRole(role)
	vendor := seat.Vendor
	if !ValidVendor(vendor) {
		vendor = fallback.Vendor
	}
	vendorDefault := fallback
	if vendor != fallback.Vendor {
		vendorDefault = DefaultSeatForVendor(vendor)
	}
	model := oneOf(seat.Model, ModelsFor(vendor), vendorDefault.Model)
	return Seat{
		Vendor:     vendor,
		Model:      model,
		Effort:     oneOf(seat.Effort, EffortsFor(vendor, model), vendorDefault.Effort),
		Permission: oneOf(seat.Permission, PermissionsFor(vendor), vendorDefault.Permission),
		extra:      cloneExtra(seat.extra),
	}
}

func normalizeSeats(seats []WorkerSeat, role *ComponentID, primary Seat, componentID string) []WorkerSeat {
	if len(seats) == 0 {
		sole := WorkerSeat{ID: WorkerSeatID(componentID, 0), Role: RoleWorker}
		sole.applyProfile(primary)
		return []WorkerSeat{sole}
	}
	if len(seats) > MaxCommitteeWorkers {
		seats = seats[:MaxCommitteeWorkers]
	}
	normalized := make([]WorkerSeat, 0, len(seats))
	used := make(map[string]struct{}, len(seats))
	for index, worker := range seats {
		profile := normalizeSeat(worker.Profile(), role)
		id := strings.TrimSpace(worker.ID)
		if len(id) > MaxIDLength {
			id = id[:MaxIDLength]
		}
		if !ValidGraphID(id) {
			id = WorkerSeatID(componentID, index)
		}
		if _, duplicate := used[id]; duplicate {
			id = fmt.Sprintf("%s-%d", id, index+1)
		}
		used[id] = struct{}{}
		next := WorkerSeat{ID: id, Role: RoleWorker, extra: cloneExtra(worker.extra)}
		if worker.Role == RoleMain {
			next.Role = RoleMain
		}
		next.applyProfile(profile)
		normalized = append(normalized, next)
	}
	applyCommitteeRoles(normalized)
	// component.seat stays authoritative for the primary profile, so a caller
	// that patched `seat` alone still reaches the worker that writes the
	// promoted artifact.
	main := primaryWorker(normalized)
	for index := range normalized {
		if normalized[index].ID == main.ID {
			normalized[index].applyProfile(primary)
		}
	}
	return normalized
}

// applyCommitteeRoles enforces the invariant that a committee has exactly one
// main when N > 1 and none when N == 1.
func applyCommitteeRoles(seats []WorkerSeat) {
	if len(seats) <= 1 {
		for index := range seats {
			seats[index].Role = RoleWorker
		}
		return
	}
	mainIndex := -1
	for index, seat := range seats {
		if seat.Role == RoleMain {
			mainIndex = index
			break
		}
	}
	if mainIndex < 0 {
		mainIndex = 0
	}
	for index := range seats {
		if index == mainIndex {
			seats[index].Role = RoleMain
			continue
		}
		seats[index].Role = RoleWorker
	}
}

// primaryWorker returns the sole worker, or the designated main when N > 1.
func primaryWorker(seats []WorkerSeat) WorkerSeat {
	if len(seats) == 0 {
		return WorkerSeat{}
	}
	if len(seats) == 1 {
		return seats[0]
	}
	for _, seat := range seats {
		if seat.Role == RoleMain {
			return seat
		}
	}
	return seats[0]
}

func normalizeCommittee(committee Committee, role *ComponentID) Committee {
	prompt := clampText(committee.ConsolidationPrompt, MaxPromptLength)
	if prompt == "" {
		prompt = DefaultCommittee(role).ConsolidationPrompt
	}
	return Committee{ConsolidationPrompt: prompt, extra: cloneExtra(committee.extra)}
}

func normalizeRequiredOutputs(outputs []string, role *ComponentID) []string {
	fallback := DefaultRequiredOutputs(role)
	if len(outputs) == 0 {
		return fallback
	}
	if len(outputs) > MaxRequiredOutputs {
		outputs = outputs[:MaxRequiredOutputs]
	}
	normalized := make([]string, 0, len(outputs))
	seen := make(map[string]struct{}, len(outputs))
	for _, output := range outputs {
		name := strings.TrimSpace(output)
		if !ValidRequiredOutput(name) {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	if len(normalized) == 0 {
		return fallback
	}
	return normalized
}

// migrateSeatTerminalBox grows a saved card to the current stacked layout.
//
// A pre-cluster seat had no companions and a narrow card. A companion-cluster
// seat had a short terminal above separate Prompt and Info cards; the UI now
// stacks those sections inside one node, so a short box needs the taller
// default. Both grow exactly once, because the grown box no longer matches
// either condition.
func migrateSeatTerminalBox(component AgentComponent, fallback NodeBox) NodeBox {
	hasCompanions := !isZeroBox(component.PromptBox) || !isZeroBox(component.InfoBox)
	box := normalizeBox(component.Box, fallback)
	if !hasCompanions && box.Width < seatTerminalWidth*0.85 {
		grown := fallback
		grown.Collapsed = box.Collapsed
		grown.Locked = box.Locked
		return grown
	}
	if hasCompanions && box.Height < seatTerminalHeight*0.75 {
		grown := box
		grown.Width = math.Max(box.Width, seatTerminalWidth)
		grown.Height = seatTerminalHeight
		return normalizeBox(grown, fallback)
	}
	return box
}

func normalizeBox(box NodeBox, fallback NodeBox) NodeBox {
	width := clampFloat(finite(box.Width, fallback.Width), NodeMinWidth, WorldWidth)
	height := clampFloat(finite(box.Height, fallback.Height), NodeMinHeight, WorldHeight)
	return NodeBox{
		X:         clampFloat(finite(box.X, fallback.X), 0, WorldWidth-width),
		Y:         clampFloat(finite(box.Y, fallback.Y), 0, WorldHeight-height),
		Width:     width,
		Height:    height,
		Collapsed: box.Collapsed,
		Locked:    box.Locked,
		extra:     cloneExtra(box.extra),
	}
}

func normalizeEdges(edges []Edge, componentIDs map[string]struct{}) []Edge {
	normalized := make([]Edge, 0, len(edges))
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		from := strings.TrimSpace(edge.From)
		to := strings.TrimSpace(edge.To)
		if from == "" || to == "" || from == to {
			continue
		}
		if _, ok := componentIDs[from]; !ok {
			continue
		}
		if _, ok := componentIDs[to]; !ok {
			continue
		}
		key := from + "->" + to
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		// An unknown kind is dropped rather than reinterpreted; an absent kind
		// is a trigger, which is how pre-feedback boards were saved.
		if edge.Kind != "" && edge.Kind != EdgeTrigger && edge.Kind != EdgeFeedback {
			continue
		}
		seen[key] = struct{}{}
		id := strings.TrimSpace(edge.ID)
		if !ValidGraphID(id) {
			id = "edge-" + from + "-" + to
		}
		next := Edge{
			ID:    id,
			From:  from,
			To:    to,
			Kind:  EdgeTrigger,
			Mode:  normalizeTriggerMode(edge.Mode),
			extra: cloneExtra(edge.extra),
		}
		if edge.Kind == EdgeFeedback {
			next.Kind = EdgeFeedback
			next.MaxRounds = normalizeFeedbackRounds(edge.MaxRounds)
		}
		normalized = append(normalized, next)
	}
	return normalized
}

func normalizeTriggerMode(mode TriggerMode) TriggerMode {
	if mode == TriggerManual {
		return TriggerManual
	}
	return TriggerAuto
}

func normalizeFeedbackRounds(rounds uint64) uint64 {
	if rounds == MaxFeedbackRounds {
		return MaxFeedbackRounds
	}
	return DefaultFeedbackMaxRounds
}

// ensureLegacyFeedbackEdge seeds Review → Build repair when the starter trigger
// chain is present but the reverse edge is missing, which is how boards saved
// before feedback edges existed look.
func ensureLegacyFeedbackEdge(components []AgentComponent, edges []Edge) []Edge {
	build := findByLegacyRole(components, ComponentBuild)
	review := findByLegacyRole(components, ComponentReview)
	plan := findByLegacyRole(components, ComponentPlan)
	if build == nil || review == nil || plan == nil {
		return edges
	}
	hasPlanBuild, hasBuildReview := false, false
	for _, edge := range edges {
		if edge.Kind == EdgeTrigger && edge.From == plan.ID && edge.To == build.ID {
			hasPlanBuild = true
		}
		if edge.Kind == EdgeTrigger && edge.From == build.ID && edge.To == review.ID {
			hasBuildReview = true
		}
		if edge.From == review.ID && edge.To == build.ID {
			return edges
		}
	}
	if !hasPlanBuild || !hasBuildReview {
		return edges
	}
	return append(edges, Edge{
		ID:        "edge-review-build",
		From:      review.ID,
		To:        build.ID,
		Kind:      EdgeFeedback,
		Mode:      TriggerAuto,
		MaxRounds: DefaultFeedbackMaxRounds,
	})
}

func normalizeRunPolicy(board *Board) {
	if board.RunPolicy == nil {
		return
	}
	policy := RunPolicy{extra: cloneExtra(board.RunPolicy.extra)}
	checks := board.RunPolicy.Checks
	if len(checks) > MaxChecks {
		checks = checks[:MaxChecks]
	}
	policy.Checks = make([]Check, 0, len(checks))
	for _, check := range checks {
		normalized, ok := normalizeCheck(check)
		if !ok {
			continue
		}
		policy.Checks = append(policy.Checks, normalized)
	}
	policy.Publish = normalizePublish(board.RunPolicy.Publish)
	if len(policy.Checks) == 0 && policy.Publish.Base == "" && !policy.Publish.Draft &&
		len(policy.extra) == 0 && len(policy.Publish.extra) == 0 {
		board.RunPolicy = nil
		return
	}
	board.RunPolicy = &policy
}

func normalizeCheck(check Check) (Check, bool) {
	name := strings.TrimSpace(check.Name)
	if !ValidCheckName(name) {
		return Check{}, false
	}
	if len(check.Argv) == 0 || len(check.Argv) > MaxArgvTokens {
		return Check{}, false
	}
	if !ValidCheckCommand(check.Argv[0]) {
		return Check{}, false
	}
	for _, token := range check.Argv {
		if !ValidArgvToken(token) {
			return Check{}, false
		}
	}
	return Check{
		Name:  name,
		Argv:  append([]string(nil), check.Argv...),
		extra: cloneExtra(check.extra),
	}, true
}

func normalizePublish(publish PublishConfig) PublishConfig {
	base := strings.TrimSpace(publish.Base)
	if !ValidBaseBranch(base) {
		base = ""
	}
	return PublishConfig{Base: base, Draft: publish.Draft, extra: cloneExtra(publish.extra)}
}

func normalizeViewport(viewport *Viewport) {
	viewport.Zoom = clampFloat(finite(viewport.Zoom, DefaultZoom), MinZoom, MaxZoom)
	viewport.PanX = finite(viewport.PanX, 0)
	viewport.PanY = finite(viewport.PanY, 0)
}

// ---------------------------------------------------------------------------
// Graph queries
// ---------------------------------------------------------------------------

// ComponentByID returns the seat with an identifier, or nil.
func (b *Board) ComponentByID(id string) *AgentComponent {
	for index := range b.Components {
		if b.Components[index].ID == id {
			return &b.Components[index]
		}
	}
	return nil
}

// ComponentByLegacyRole returns the seat bound to a run stage, or nil.
func (b *Board) ComponentByLegacyRole(role ComponentID) *AgentComponent {
	return findByLegacyRole(b.Components, role)
}

func findByLegacyRole(components []AgentComponent, role ComponentID) *AgentComponent {
	for index := range components {
		if components[index].LegacyRole != nil && *components[index].LegacyRole == role {
			return &components[index]
		}
	}
	return nil
}

// IsRunnableLegacyGraph reports whether the board still maps to the classic
// runnable chain: plan → build → review through legacyRole seats and matching
// trigger edges.
func (b *Board) IsRunnableLegacyGraph() bool {
	plan := b.ComponentByLegacyRole(ComponentPlan)
	build := b.ComponentByLegacyRole(ComponentBuild)
	review := b.ComponentByLegacyRole(ComponentReview)
	if plan == nil || build == nil || review == nil {
		return false
	}
	if plan.ID == build.ID || build.ID == review.ID || plan.ID == review.ID {
		return false
	}
	return b.hasTriggerEdge(plan.ID, build.ID) && b.hasTriggerEdge(build.ID, review.ID)
}

func (b *Board) hasTriggerEdge(from, to string) bool {
	for _, edge := range b.Edges {
		if edge.Kind == EdgeTrigger && edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

// RunnableBlockedReason explains why a board cannot start a run, or returns an
// empty string when it can.
func (b *Board) RunnableBlockedReason() string {
	if b.IsRunnableLegacyGraph() {
		return ""
	}
	return "Custom graph runtime coming — Run only works on the plan → build → review starter chain"
}

// FeedbackMaxRoundsToBuild returns the automatic repair budget for Build.
//
// Feedback edges targeting the Build seat set the cap. A board with no feedback
// edge to Build allows zero automatic repairs and gates immediately, which is
// the deliberate difference from a v1 board: v1 had no edges to express the
// choice, so it keeps the default of one.
func (b *Board) FeedbackMaxRoundsToBuild() uint64 {
	build := b.ComponentByLegacyRole(ComponentBuild)
	buildID := "build"
	if build != nil {
		buildID = build.ID
	}
	found := false
	var maximum uint64
	for _, edge := range b.Edges {
		if edge.Kind != EdgeFeedback || edge.To != buildID {
			continue
		}
		found = true
		if rounds := normalizeFeedbackRounds(edge.MaxRounds); rounds > maximum {
			maximum = rounds
		}
	}
	if !found {
		return 0
	}
	return maximum
}

// FeedbackModeToBuild reports whether a repair hop waits for an explicit Go.
// Any manual feedback edge into Build makes the hop manual.
func (b *Board) FeedbackModeToBuild() TriggerMode {
	build := b.ComponentByLegacyRole(ComponentBuild)
	buildID := "build"
	if build != nil {
		buildID = build.ID
	}
	for _, edge := range b.Edges {
		if edge.Kind == EdgeFeedback && edge.To == buildID && edge.Mode == TriggerManual {
			return TriggerManual
		}
	}
	return TriggerAuto
}

// TriggerModeBetween returns the mode of the trigger edge between two run
// stages. A missing edge is `auto`, which is how v1 boards and pre-mode
// snapshots behaved.
func (b *Board) TriggerModeBetween(from, to ComponentID) TriggerMode {
	fromID, toID := string(from), string(to)
	if component := b.ComponentByLegacyRole(from); component != nil {
		fromID = component.ID
	}
	if component := b.ComponentByLegacyRole(to); component != nil {
		toID = component.ID
	}
	for _, edge := range b.Edges {
		if edge.Kind != EdgeTrigger || edge.From != fromID || edge.To != toID {
			continue
		}
		return normalizeTriggerMode(edge.Mode)
	}
	return TriggerAuto
}

// SeatPromptNodeID and SeatInfoNodeID derive a companion card's node id.
func SeatPromptNodeID(componentID string) string { return componentID + "-prompt" }

// SeatInfoNodeID derives the info companion card's node id.
func SeatInfoNodeID(componentID string) string { return componentID + "-info" }

// SeatRole names which card of a seat cluster a node id refers to.
type SeatRole string

const (
	SeatTerminal SeatRole = "terminal"
	SeatPrompt   SeatRole = "prompt"
	SeatInfo     SeatRole = "info"
)

// ParseNodeID splits a canvas node id into its component and card.
func ParseNodeID(nodeID string) (string, SeatRole) {
	if strings.HasSuffix(nodeID, "-prompt") {
		return strings.TrimSuffix(nodeID, "-prompt"), SeatPrompt
	}
	if strings.HasSuffix(nodeID, "-info") {
		return strings.TrimSuffix(nodeID, "-info"), SeatInfo
	}
	return nodeID, SeatTerminal
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func clampText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	// Cut on a rune boundary so a clamped prompt is still valid UTF-8.
	trimmed := value[:limit]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

// isZeroBox reports an absent companion card. A saved Prompt or Info box always
// carries a positive size, so the zero value is only ever produced by a member
// the document did not contain.
func isZeroBox(box NodeBox) bool {
	return box.X == 0 && box.Y == 0 && box.Width == 0 && box.Height == 0 &&
		!box.Collapsed && !box.Locked
}

func oneOf(value string, allowed []string, fallback string) string {
	for _, candidate := range allowed {
		if candidate == value {
			return value
		}
	}
	return fallback
}

func finite(value, fallback float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	return value
}

func clampFloat(value, minimum, maximum float64) float64 {
	if maximum < minimum {
		maximum = minimum
	}
	return math.Max(minimum, math.Min(maximum, value))
}
