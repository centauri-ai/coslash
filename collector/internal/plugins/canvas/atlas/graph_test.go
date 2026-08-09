package atlas

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return contents
}

func decodeFixtureBoard(t *testing.T, name string) *Board {
	t.Helper()
	board, err := DecodeBoard(readFixture(t, name))
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return board
}

// jsonEqual compares documents by value, so a difference in member order never
// fails a test that is about content.
func jsonEqual(t *testing.T, got, want []byte) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal(got, &left); err != nil {
		t.Fatalf("decode got: %v", err)
	}
	if err := json.Unmarshal(want, &right); err != nil {
		t.Fatalf("decode want: %v", err)
	}
	return reflect.DeepEqual(left, right)
}

// sameBox compares placement and display state. NodeBox carries a map of
// preserved members, so it is not comparable with ==.
func sameBox(a, b NodeBox) bool {
	return a.X == b.X && a.Y == b.Y && a.Width == b.Width && a.Height == b.Height &&
		a.Collapsed == b.Collapsed && a.Locked == b.Locked
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

func TestDecodeGoldenV2Board(t *testing.T) {
	board := decodeFixtureBoard(t, "board-v2.json")

	if board.Kind != BoardKind || board.SchemaVersion != BoardSchemaVersion {
		t.Fatalf("board identity = %q/%d", board.Kind, board.SchemaVersion)
	}
	if len(board.Components) != 3 {
		t.Fatalf("components = %d, want 3", len(board.Components))
	}
	plan := board.ComponentByLegacyRole(ComponentPlan)
	if plan == nil {
		t.Fatal("plan seat is missing")
	}
	if len(plan.Seats) != 2 {
		t.Fatalf("plan committee = %d workers, want 2", len(plan.Seats))
	}
	if plan.Seats[0].Role != RoleMain || plan.Seats[1].Role != RoleWorker {
		t.Fatalf("committee roles = %q/%q", plan.Seats[0].Role, plan.Seats[1].Role)
	}
	if plan.Seats[1].Vendor != VendorCodex || plan.Seats[1].Model != "gpt-5.6-terra" {
		t.Fatalf("sibling worker profile = %q/%q", plan.Seats[1].Vendor, plan.Seats[1].Model)
	}
	// The frozen reference geometry is the visual contract for the migration.
	if !sameBox(plan.Box, NodeBox{X: 120, Y: 160, Width: 440, Height: 760}) {
		t.Fatalf("plan box = %+v", plan.Box)
	}
	if plan.PromptBox.Y != 948 || plan.InfoBox.X != 520 {
		t.Fatalf("companion layout = %+v / %+v", plan.PromptBox, plan.InfoBox)
	}
	if board.FeedbackMaxRoundsToBuild() != 1 {
		t.Fatalf("repair budget = %d, want 1", board.FeedbackMaxRoundsToBuild())
	}
	if mode := board.TriggerModeBetween(ComponentBuild, ComponentReview); mode != TriggerManual {
		t.Fatalf("build → review mode = %q, want manual", mode)
	}
	if !board.IsRunnableLegacyGraph() {
		t.Fatal("the golden board should be runnable")
	}
	if err := AssertPolicy(board); err != nil {
		t.Fatalf("golden board violates policy: %v", err)
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	for _, name := range []string{"board-v1.json", "board-v2.json"} {
		t.Run(name, func(t *testing.T) {
			board := decodeFixtureBoard(t, name)
			once := mustMarshal(t, board)

			Normalize(board)
			twice := mustMarshal(t, board)
			if !jsonEqual(t, twice, once) {
				t.Fatalf("second normalization changed the board:\n%s\n%s", once, twice)
			}

			// A decode of the encoded form must also be stable, which is what
			// makes a save-reload cycle safe.
			reloaded, err := DecodeBoard(once)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if !jsonEqual(t, mustMarshal(t, reloaded), once) {
				t.Fatal("reloading a normalized board changed it")
			}
		})
	}
}

func TestNormalizeRepairsUntrustedGraph(t *testing.T) {
	raw := []byte(`{
	  "kind": "atlas",
	  "schemaVersion": 2,
	  "components": [
	    {"id": "plan", "legacyRole": "plan", "seat": {"vendor": "claude", "model": "gpt-4", "effort": "warp", "permission": "manual"}},
	    {"id": "plan", "legacyRole": "build", "seat": {"vendor": "claude", "model": "opus"}},
	    {"id": "build", "legacyRole": "build", "seat": {"vendor": "claude", "model": "opus", "effort": "high", "permission": "acceptEdits"}},
	    {"id": "review-prompt", "legacyRole": "review"},
	    {"id": "", "legacyRole": null}
	  ],
	  "edges": [
	    {"id": "a", "from": "plan", "to": "build", "kind": "trigger"},
	    {"id": "b", "from": "plan", "to": "build", "kind": "trigger"},
	    {"id": "c", "from": "plan", "to": "ghost", "kind": "trigger"},
	    {"id": "d", "from": "plan", "to": "plan", "kind": "trigger"},
	    {"id": "e", "from": "build", "to": "plan", "kind": "sideways"}
	  ],
	  "viewport": {"zoom": 99, "panX": 5}
	}`)

	board, err := DecodeBoard(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	ids := make([]string, 0, len(board.Components))
	for _, component := range board.Components {
		ids = append(ids, component.ID)
	}
	// The duplicate id, the companion-colliding id, and the unusable id all drop.
	if got := strings.Join(ids, ","); got != "plan,build" {
		t.Fatalf("components = %q, want \"plan,build\"", got)
	}

	plan := board.ComponentByID("plan")
	if plan.Seat.Model != "opus" || plan.Seat.Effort != "high" || plan.Seat.Permission != "acceptEdits" {
		t.Fatalf("unknown launch values were not repaired: %+v", plan.Seat)
	}
	if len(plan.Seats) != 1 || plan.Seats[0].Role != RoleWorker {
		t.Fatalf("a missing committee should become one worker: %+v", plan.Seats)
	}
	if plan.Seats[0].ID != "plan-worker-1" {
		t.Fatalf("worker id = %q, want a deterministic derived id", plan.Seats[0].ID)
	}

	// Duplicate, dangling, self, and unknown-kind edges all drop.
	if len(board.Edges) != 1 || board.Edges[0].ID != "a" {
		t.Fatalf("edges = %+v, want only the first plan → build trigger", board.Edges)
	}
	if board.Viewport.Zoom != MaxZoom || board.Viewport.PanX != 5 {
		t.Fatalf("viewport = %+v", board.Viewport)
	}
}

func TestNormalizeCommitteeRoles(t *testing.T) {
	tests := []struct {
		name      string
		seats     string
		wantRoles []WorkerRole
	}{
		{"sole worker never main", `[{"id":"a","role":"main","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"}]`, []WorkerRole{RoleWorker}},
		{"first becomes main", `[{"id":"a","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"},{"id":"b","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"}]`, []WorkerRole{RoleMain, RoleWorker}},
		{"one main survives", `[{"id":"a","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"},{"id":"b","role":"main","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"},{"id":"c","role":"main","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"}]`, []WorkerRole{RoleWorker, RoleMain, RoleWorker}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(`{"kind":"atlas","schemaVersion":2,"components":[{"id":"plan","legacyRole":"plan","seat":{"vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"},"seats":` + test.seats + `}],"edges":[],"viewport":{"zoom":0.55}}`)
			board, err := DecodeBoard(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			plan := board.ComponentByID("plan")
			if len(plan.Seats) != len(test.wantRoles) {
				t.Fatalf("seats = %d, want %d", len(plan.Seats), len(test.wantRoles))
			}
			for index, want := range test.wantRoles {
				if plan.Seats[index].Role != want {
					t.Fatalf("seats[%d].role = %q, want %q", index, plan.Seats[index].Role, want)
				}
			}
			if err := AssertPolicy(board); err != nil {
				t.Fatalf("a normalized committee must satisfy policy: %v", err)
			}
		})
	}
}

func TestNormalizeDeduplicatesWorkerIDsAndOutputs(t *testing.T) {
	raw := []byte(`{"kind":"atlas","schemaVersion":2,"components":[{
	  "id":"plan","legacyRole":"plan",
	  "seat":{"vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"},
	  "seats":[
	    {"id":"same","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"},
	    {"id":"same","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits"}
	  ],
	  "requiredOutputs":["PLAN.md","PLAN.md","../escape","ok.md"]
	}],"edges":[],"viewport":{"zoom":0.55}}`)

	board, err := DecodeBoard(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	plan := board.ComponentByID("plan")
	if plan.Seats[0].ID == plan.Seats[1].ID {
		t.Fatalf("duplicate worker ids survived: %+v", plan.Seats)
	}
	want := []string{"PLAN.md", "ok.md"}
	if !reflect.DeepEqual(plan.RequiredOutputs, want) {
		t.Fatalf("requiredOutputs = %v, want %v", plan.RequiredOutputs, want)
	}
	if err := AssertPolicy(board); err != nil {
		t.Fatalf("policy: %v", err)
	}
}

func TestNormalizeGrowsLegacyCardsExactlyOnce(t *testing.T) {
	// A pre-cluster card: narrow, no companions.
	narrow := []byte(`{"kind":"atlas","schemaVersion":2,"components":[{"id":"plan","legacyRole":"plan","box":{"x":40,"y":50,"width":300,"height":200,"locked":true}}],"edges":[],"viewport":{"zoom":0.55}}`)
	board, err := DecodeBoard(narrow)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	grown := board.ComponentByID("plan").Box
	if grown.Width != 440 || grown.Height != 760 || !grown.Locked {
		t.Fatalf("narrow card did not grow to the stacked default: %+v", grown)
	}

	Normalize(board)
	if !sameBox(board.ComponentByID("plan").Box, grown) {
		t.Fatalf("card grew twice: %+v", board.ComponentByID("plan").Box)
	}

	// A companion-cluster card: short terminal above separate cards.
	short := []byte(`{"kind":"atlas","schemaVersion":2,"components":[{"id":"plan","legacyRole":"plan","box":{"x":10,"y":20,"width":460,"height":300},"promptBox":{"x":10,"y":340,"width":380,"height":260},"infoBox":{"x":410,"y":340,"width":380,"height":320}}],"edges":[],"viewport":{"zoom":0.55}}`)
	board, err = DecodeBoard(short)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	box := board.ComponentByID("plan").Box
	if box.Height != 760 || box.Width != 460 || box.X != 10 {
		t.Fatalf("short clustered card did not grow in place: %+v", box)
	}
	Normalize(board)
	if !sameBox(board.ComponentByID("plan").Box, box) {
		t.Fatalf("clustered card grew twice: %+v", board.ComponentByID("plan").Box)
	}
}

func TestNormalizeClampsBoxesIntoTheWorld(t *testing.T) {
	raw := []byte(`{"kind":"atlas","schemaVersion":2,"components":[{"id":"plan","legacyRole":"plan","box":{"x":-500,"y":99999,"width":99999,"height":1},"promptBox":{"x":1,"y":1,"width":380,"height":260}}],"edges":[],"viewport":{"zoom":0.55}}`)
	board, err := DecodeBoard(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	box := board.ComponentByID("plan").Box
	if box.X < 0 || box.Y < 0 || box.X+box.Width > WorldWidth || box.Y+box.Height > WorldHeight {
		t.Fatalf("box escaped the world: %+v", box)
	}
	if box.Width < NodeMinWidth || box.Height < NodeMinHeight {
		t.Fatalf("box below the minimum size: %+v", box)
	}
}

func TestBoardPreservesUnknownMembers(t *testing.T) {
	raw := []byte(`{
	  "kind":"atlas","schemaVersion":2,"futureBoardField":{"a":1},
	  "components":[{
	    "id":"plan","legacyRole":"plan","futureSeatField":"keep",
	    "seat":{"vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits","futureSeatDetail":7},
	    "seats":[{"id":"w1","vendor":"claude","model":"opus","effort":"high","permission":"acceptEdits","futureWorkerField":true}],
	    "box":{"x":120,"y":160,"width":440,"height":760,"futureBoxField":"b"}
	  }],
	  "edges":[{"id":"a","from":"plan","to":"plan2","kind":"trigger","futureEdgeField":1}],
	  "viewport":{"zoom":0.55,"futureViewportField":2}
	}`)

	board, err := DecodeBoard(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	encoded := mustMarshal(t, board)

	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		t.Fatalf("decode encoded board: %v", err)
	}
	if _, ok := members["futureBoardField"]; !ok {
		t.Fatalf("unknown board member was dropped: %s", encoded)
	}
	for _, needle := range []string{"futureSeatField", "futureSeatDetail", "futureWorkerField", "futureBoxField", "futureViewportField"} {
		if !strings.Contains(string(encoded), needle) {
			t.Fatalf("unknown member %s was dropped: %s", needle, encoded)
		}
	}
	// The dangling edge still drops; preservation is about members this build
	// does not model, not about content it has judged.
	if strings.Contains(string(encoded), "futureEdgeField") {
		t.Fatalf("a dropped edge carried its members back: %s", encoded)
	}
}

func TestDeclaredMembersWinOverPreservedOnes(t *testing.T) {
	seat := Seat{Vendor: VendorClaude, Model: "opus", Effort: "high", Permission: "acceptEdits"}
	seat.extra = map[string]json.RawMessage{"model": json.RawMessage(`"smuggled"`)}
	encoded := mustMarshal(t, seat)
	if strings.Contains(string(encoded), "smuggled") {
		t.Fatalf("a preserved member shadowed a declared one: %s", encoded)
	}
}

func TestParseNodeID(t *testing.T) {
	tests := []struct {
		nodeID    string
		component string
		role      SeatRole
	}{
		{"plan", "plan", SeatTerminal},
		{SeatPromptNodeID("plan"), "plan", SeatPrompt},
		{SeatInfoNodeID("plan"), "plan", SeatInfo},
	}
	for _, test := range tests {
		component, role := ParseNodeID(test.nodeID)
		if component != test.component || role != test.role {
			t.Fatalf("ParseNodeID(%q) = %q/%q, want %q/%q", test.nodeID, component, role, test.component, test.role)
		}
	}
}

func TestRunnableBlockedReason(t *testing.T) {
	board := DefaultBoard()
	if reason := board.RunnableBlockedReason(); reason != "" {
		t.Fatalf("the starter board should be runnable, got %q", reason)
	}
	board.Edges = nil
	if board.RunnableBlockedReason() == "" {
		t.Fatal("a board with no trigger chain must report why Run is blocked")
	}
}

func TestDefaultBoardSatisfiesPolicy(t *testing.T) {
	board := DefaultBoard()
	Normalize(board)
	if err := AssertPolicy(board); err != nil {
		t.Fatalf("the starter board must satisfy policy: %v", err)
	}
	if err := AssertRunnable(board); err != nil {
		t.Fatalf("the starter board must be runnable: %v", err)
	}
	if board.RunPolicy != nil {
		t.Fatalf("a starter board should carry no run policy: %+v", board.RunPolicy)
	}
}

func TestSystemPromptUpgradeAndPlaceholders(t *testing.T) {
	superseded := supersededSystemPrompts[0]
	raw := []byte(`{"kind":"atlas","schemaVersion":2,"systemPrompts":{"plan":` +
		string(mustMarshal(t, superseded)) + `,"build":"","review":"custom review","planRefine":"   "},` +
		`"components":[{"id":"plan","legacyRole":"plan"}],"edges":[],"viewport":{"zoom":0.55}}`)

	board, err := DecodeBoard(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if board.SystemPrompts.Plan != DefaultPlanPrompt {
		t.Fatal("an untouched superseded default should upgrade")
	}
	if board.SystemPrompts.Build != DefaultBuildPrompt || board.SystemPrompts.PlanRefine != DefaultPlanRefinePrompt {
		t.Fatal("a blank prompt should fall back to the current default")
	}
	if board.SystemPrompts.Review != "custom review" {
		t.Fatalf("an edited prompt must survive, got %q", board.SystemPrompts.Review)
	}

	applied := ApplySystemPromptPlaceholders(
		"write {{OUTPUT_NAME}} to {{OUTPUT_PATH}} and json to {{OUTPUT_JSON_PATH}}",
		".fleetlog/run/PLAN.md", ".fleetlog/run/plan.json", "")
	want := "write PLAN.md to .fleetlog/run/PLAN.md and json to .fleetlog/run/plan.json"
	if applied != want {
		t.Fatalf("placeholders = %q, want %q", applied, want)
	}
}

func TestClampTextKeepsValidUTF8(t *testing.T) {
	value := strings.Repeat("é", MaxTitleLength)
	clamped := clampText(value, MaxTitleLength)
	if len(clamped) > MaxTitleLength {
		t.Fatalf("clamped length = %d, want at most %d", len(clamped), MaxTitleLength)
	}
	if !isValidUTF8(clamped) {
		t.Fatalf("clamping split a rune: %q", clamped)
	}
}

func isValidUTF8(value string) bool {
	for _, r := range value {
		if r == '�' {
			return false
		}
	}
	return true
}
