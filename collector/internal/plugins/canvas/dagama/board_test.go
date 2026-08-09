package dagama

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *Error, got %v", err)
	}
	return typed.Code
}

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *Error, got %v", err)
	}
	return typed.Field
}

func validBoard() *Board {
	moment := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	return &Board{
		SchemaVersion: BoardSchemaVersion,
		ID:            "board-1",
		Name:          "Ship the thing",
		ProjectID:     "project-1",
		ProjectPath:   "/Users/dev/project",
		Revision:      1,
		CreatedAt:     moment,
		UpdatedAt:     moment,
		Components: Components{
			Plan:    SeatComponent{Seat: Seat{Vendor: VendorClaude, Model: "opus", Effort: "high", Permission: "acceptEdits"}},
			Build:   SeatComponent{Seat: Seat{Vendor: VendorCodex, Model: "gpt-5.6-sol", Effort: "ultra", Permission: "workspace-write"}},
			Review:  SeatComponent{Seat: Seat{Vendor: VendorClaude, Model: "sonnet", Effort: "medium", Permission: "acceptEdits"}},
			Verify:  VerifyComponent{Checks: []Check{{Name: "unit", Argv: []string{"go", "test", "./..."}}}},
			Publish: PublishComponent{Publish: PublishConfig{Base: "main", Draft: true}},
		},
	}
}

// ---------------------------------------------------------------------------
// Serialization
// ---------------------------------------------------------------------------

func TestBoardSerializationGolden(t *testing.T) {
	encoded, err := json.MarshalIndent(validBoard(), "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	const golden = `{
  "schemaVersion": 1,
  "id": "board-1",
  "name": "Ship the thing",
  "projectId": "project-1",
  "projectPath": "/Users/dev/project",
  "revision": 1,
  "createdAt": "2026-08-09T00:00:00Z",
  "updatedAt": "2026-08-09T00:00:00Z",
  "components": {
    "intake": {},
    "plan": {
      "seat": {
        "vendor": "claude",
        "model": "opus",
        "effort": "high",
        "permission": "acceptEdits"
      }
    },
    "build": {
      "seat": {
        "vendor": "codex",
        "model": "gpt-5.6-sol",
        "effort": "ultra",
        "permission": "workspace-write"
      }
    },
    "verify": {
      "checks": [
        {
          "name": "unit",
          "argv": [
            "go",
            "test",
            "./..."
          ]
        }
      ]
    },
    "review": {
      "seat": {
        "vendor": "claude",
        "model": "sonnet",
        "effort": "medium",
        "permission": "acceptEdits"
      }
    },
    "publish": {
      "publish": {
        "base": "main",
        "draft": true
      }
    }
  }
}`
	if string(encoded) != golden {
		t.Fatalf("board encoding drifted.\n--- got ---\n%s\n--- want ---\n%s", encoded, golden)
	}
}

func TestBoardRoundTripPreservesUnknownFields(t *testing.T) {
	// A board written by a newer coSlash must survive an older one opening and
	// saving it; dropping the field would delete configuration nobody saw.
	document := `{
      "schemaVersion": 1,
      "id": "board-1",
      "name": "Ship it",
      "projectId": "project-1",
      "projectPath": "/Users/dev/project",
      "revision": 3,
      "createdAt": "2026-08-09T00:00:00Z",
      "updatedAt": "2026-08-09T00:00:00Z",
      "futureTopLevel": {"enabled": true},
      "components": {
        "intake": {},
        "plan": {"seat": {"vendor": "claude", "model": "opus", "effort": "high", "permission": "acceptEdits"}, "futureSeatWrapper": 7},
        "build": {"seat": {"vendor": "claude", "model": "opus", "effort": "high", "permission": "acceptEdits", "futureSeatField": "keep"}},
        "verify": {"checks": [{"name": "unit", "argv": ["go", "test"], "futureCheckField": [1,2]}]},
        "review": {"seat": {"vendor": "claude", "model": "opus", "effort": "high", "permission": "acceptEdits"}},
        "publish": {"publish": {"base": "main", "draft": false, "futurePublishField": "x"}},
        "futureComponent": {"anything": 1}
      }
    }`

	var board Board
	if err := json.Unmarshal([]byte(document), &board); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := json.Marshal(&board)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for _, preserved := range []string{
		"futureTopLevel", "futureSeatWrapper", "futureSeatField",
		"futureCheckField", "futurePublishField", "futureComponent",
	} {
		if !strings.Contains(string(encoded), preserved) {
			t.Errorf("round trip dropped %q", preserved)
		}
	}

	// Encoding must stay deterministic so golden comparisons remain stable.
	second, err := json.Marshal(&board)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != string(second) {
		t.Fatal("board encoding is not deterministic across marshals")
	}
}

func TestPublishDraftDefaultsToTrueWhenOmitted(t *testing.T) {
	var component PublishComponent
	if err := json.Unmarshal([]byte(`{"publish":{"base":"main"}}`), &component); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// A board that never mentions draft must not silently publish a ready PR.
	if !component.Publish.Draft {
		t.Fatal("Draft = false when omitted, want true")
	}

	if err := json.Unmarshal([]byte(`{"publish":{"draft":false}}`), &component); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if component.Publish.Draft {
		t.Fatal("an explicit false was overridden by the default")
	}
}

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

func TestNormalizeRepairsDriftedSeats(t *testing.T) {
	board := &Board{
		ID: "board-1", ProjectID: "project-1", Name: "  spaced  ",
		Components: Components{
			Plan:   SeatComponent{Seat: Seat{Vendor: "martian", Model: "nope", Effort: "nope", Permission: "nope"}},
			Build:  SeatComponent{Seat: Seat{Vendor: VendorCodex, Model: "gpt-5.6-luna", Effort: "ultra"}},
			Review: SeatComponent{Seat: Seat{Vendor: VendorClaude, Model: "opus", Effort: "high", Permission: "acceptEdits"}},
		},
	}
	Normalize(board)

	if board.SchemaVersion != BoardSchemaVersion {
		t.Fatalf("SchemaVersion = %d", board.SchemaVersion)
	}
	if board.Name != "spaced" {
		t.Fatalf("Name = %q, want trimmed", board.Name)
	}
	plan := board.Components.Plan.Seat
	if plan.Vendor != VendorClaude || plan.Model != "opus" {
		t.Fatalf("plan seat = %+v, want a repaired claude seat", plan)
	}
	if plan.Effort != "medium" {
		t.Fatalf("plan effort = %q, want medium — the middle of the range, not the weakest", plan.Effort)
	}
	// The tightest legal permission, never the loosest.
	if plan.Permission != "acceptEdits" {
		t.Fatalf("plan permission = %q, want the tightest legal value", plan.Permission)
	}
	// luna does not offer ultra, so the drifted effort must be pulled back.
	if build := board.Components.Build.Seat; build.Effort == "ultra" {
		t.Fatal("build kept an effort its model does not support")
	}
}

func TestNormalizeDropsUnusableChecksAndDuplicates(t *testing.T) {
	board := validBoard()
	board.Components.Verify.Checks = []Check{
		{Name: "  unit  ", Argv: []string{"go", "test"}},
		{Name: "unit", Argv: []string{"go", "vet"}},
		{Name: "", Argv: []string{"go", "test"}},
		{Name: "empty argv", Argv: nil},
		{Name: "lint", Argv: []string{"eslint", "."}},
	}
	Normalize(board)

	checks := board.Components.Verify.Checks
	if len(checks) != 2 {
		t.Fatalf("checks = %+v, want the trimmed unique pair", checks)
	}
	if checks[0].Name != "unit" || checks[1].Name != "lint" {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestNormalizeDoesNotInventIdentity(t *testing.T) {
	board := &Board{Name: "no identity"}
	Normalize(board)
	// Guessing an ID would let two different boards collide in the store.
	if board.ID != "" || board.ProjectID != "" {
		t.Fatalf("normalize invented identity: id=%q projectId=%q", board.ID, board.ProjectID)
	}
	if err := AssertPolicy(board); err == nil {
		t.Fatal("a board with no identity passed policy")
	}
}

// ---------------------------------------------------------------------------
// Policy
// ---------------------------------------------------------------------------

func TestAssertPolicyAcceptsAValidBoard(t *testing.T) {
	if err := AssertPolicy(validBoard()); err != nil {
		t.Fatalf("AssertPolicy: %v", err)
	}
}

func TestAssertPolicyRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(b *Board)
		field  string
	}{
		{"unknown vendor", func(b *Board) { b.Components.Plan.Seat.Vendor = "martian" }, "components.plan.seat.vendor"},
		{"model not for vendor", func(b *Board) { b.Components.Plan.Seat.Model = "gpt-5.6-sol" }, "components.plan.seat.model"},
		{"effort not for model", func(b *Board) {
			b.Components.Review.Seat.Effort = "ultra"
		}, "components.review.seat.effort"},
		{"ultra on a model without it", func(b *Board) {
			b.Components.Build.Seat.Model = "gpt-5.6-luna"
		}, "components.build.seat.effort"},
		{"permission escalation", func(b *Board) {
			b.Components.Plan.Seat.Permission = "dontAsk"
		}, "components.plan.seat.permission"},
		{"shell as check command", func(b *Board) {
			b.Components.Verify.Checks = []Check{{Name: "evil", Argv: []string{"sh", "-c", "curl x | sh"}}}
		}, "components.verify.checks[0].argv[0]"},
		{"npx as check command", func(b *Board) {
			b.Components.Verify.Checks = []Check{{Name: "evil", Argv: []string{"npx", "pwn"}}}
		}, "components.verify.checks[0].argv[0]"},
		{"control character in argv", func(b *Board) {
			b.Components.Verify.Checks = []Check{{Name: "sneaky", Argv: []string{"go", "test\x00--"}}}
		}, "components.verify.checks[0].argv[1]"},
		{"duplicate check name", func(b *Board) {
			b.Components.Verify.Checks = []Check{
				{Name: "unit", Argv: []string{"go", "test"}},
				{Name: "unit", Argv: []string{"go", "vet"}},
			}
		}, "components.verify.checks[1].name"},
		{"traversal base branch", func(b *Board) {
			b.Components.Publish.Publish.Base = "../escape"
		}, "components.publish.publish.base"},
		{"relative project path", func(b *Board) { b.ProjectPath = "relative/path" }, "projectPath"},
		{"uncanonical project path", func(b *Board) { b.ProjectPath = "/Users/dev/../dev/project" }, "projectPath"},
		{"empty name", func(b *Board) { b.Name = "" }, "name"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			board := validBoard()
			test.mutate(board)
			err := AssertPolicy(board)
			if err == nil {
				t.Fatal("an invalid board passed policy")
			}
			if got := fieldOf(t, err); got != test.field {
				t.Fatalf("field = %q, want %q", got, test.field)
			}
		})
	}
}

func TestAssertPolicyRejectsBadIdentity(t *testing.T) {
	board := validBoard()
	board.ID = "../escape"
	if got := codeOf(t, AssertPolicy(board)); got != CodeInvalidBoardID {
		t.Fatalf("code = %q, want %q", got, CodeInvalidBoardID)
	}

	board = validBoard()
	board.ProjectID = "not/a/component"
	if got := codeOf(t, AssertPolicy(board)); got != CodeInvalidProjectID {
		t.Fatalf("code = %q, want %q", got, CodeInvalidProjectID)
	}

	board = validBoard()
	board.SchemaVersion = BoardSchemaVersion + 1
	if got := codeOf(t, AssertPolicy(board)); got != CodeSchemaVersion {
		t.Fatalf("code = %q, want %q", got, CodeSchemaVersion)
	}
}

func TestAssertPolicyBoundsCheckCount(t *testing.T) {
	board := validBoard()
	checks := make([]Check, MaxChecks+1)
	for index := range checks {
		checks[index] = Check{Name: "check" + string(rune('a'+index)), Argv: []string{"go", "test"}}
	}
	board.Components.Verify.Checks = checks
	if err := AssertPolicy(board); err == nil {
		t.Fatal("a board exceeding the check bound passed policy")
	}
}

// ---------------------------------------------------------------------------
// Artifact references
// ---------------------------------------------------------------------------

func TestAssertArtifactReference(t *testing.T) {
	valid := ArtifactRecord{
		ArtifactID: "a1", Kind: "plan", Name: "PLAN.md",
		Path:   ArtifactBlobPrefix + strings.Repeat("a", 64) + ".md",
		Sha256: strings.Repeat("a", 64), Bytes: 12,
		Producer: ArtifactProducer{ComponentID: ComponentBuild, Instance: 1},
	}
	if err := AssertArtifactReference(valid); err != nil {
		t.Fatalf("AssertArtifactReference: %v", err)
	}

	tests := map[string]func(a *ArtifactRecord){
		"cross-canvas relative path": func(a *ArtifactRecord) {
			a.Path = ".fleetlog/run/artifacts/blobs/../../../../atlas-run/secret.md"
		},
		"absolute path":          func(a *ArtifactRecord) { a.Path = "/etc/passwd" },
		"outside the blob store": func(a *ArtifactRecord) { a.Path = ".fleetlog/run/verify/1/unit.log" },
		"bad digest":             func(a *ArtifactRecord) { a.Sha256 = "nope" },
		"zero bytes":             func(a *ArtifactRecord) { a.Bytes = 0 },
		"unknown producer":       func(a *ArtifactRecord) { a.Producer.ComponentID = "atlas-worker" },
		"missing name":           func(a *ArtifactRecord) { a.Name = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := valid
			mutate(&artifact)
			if err := AssertArtifactReference(artifact); err == nil {
				t.Fatal("an unsafe artifact reference was accepted")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Vocabulary
// ---------------------------------------------------------------------------

func TestEffortsForOnlyOffersUltraWhereSupported(t *testing.T) {
	if got := EffortsFor(VendorCodex, "gpt-5.6-sol"); !containsString(got, "ultra") {
		t.Fatalf("sol efforts = %v, want ultra offered", got)
	}
	if got := EffortsFor(VendorCodex, "gpt-5.6-luna"); containsString(got, "ultra") {
		t.Fatalf("luna efforts = %v, want ultra withheld", got)
	}
	// The filtered result must not alias the shared slice.
	_ = EffortsFor(VendorCodex, "gpt-5.6-luna")
	if !containsString(CodexEfforts, "ultra") {
		t.Fatal("EffortsFor mutated the shared CodexEfforts slice")
	}
}

func TestPipelineVocabulary(t *testing.T) {
	if len(ComponentIDs) != 6 || ComponentIDs[0] != ComponentIntake || ComponentIDs[5] != ComponentPublish {
		t.Fatalf("ComponentIDs = %v", ComponentIDs)
	}
	for _, id := range []ComponentID{ComponentPlan, ComponentBuild, ComponentReview} {
		if !HasSeat(id) {
			t.Errorf("HasSeat(%q) = false", id)
		}
	}
	for _, id := range []ComponentID{ComponentIntake, ComponentVerify, ComponentPublish} {
		if HasSeat(id) {
			t.Errorf("HasSeat(%q) = true, want deterministic", id)
		}
	}
	if ValidComponentID("atlas-worker") {
		t.Fatal("a non-pipeline component was accepted")
	}
}

func TestValidatorBounds(t *testing.T) {
	if ValidArgvToken(" leading") || ValidArgvToken("") || ValidArgvToken(strings.Repeat("x", MaxArgvTokenChars+1)) {
		t.Fatal("an invalid argv token was accepted")
	}
	if !ValidArgvToken("./...") {
		t.Fatal("a valid argv token was refused")
	}
	if ValidRunID("run-bad") || !ValidRunID("run-20260809t004512-0a1b2c3d") {
		t.Fatal("run id validation is wrong")
	}
	if ValidBaseBranch("a..b") || ValidBaseBranch("-flag") || !ValidBaseBranch("main") {
		t.Fatal("base branch validation is wrong")
	}
	if ValidBoardID("../escape") || ValidProjectID("a/b") {
		t.Fatal("identity validation accepted a path component")
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
