package atlas

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestMigrateGoldenV1BoardIsIdempotent(t *testing.T) {
	board, err := DecodeBoard(readFixture(t, "board-v1.json"))
	if err != nil {
		t.Fatalf("decode v1: %v", err)
	}
	if board.SchemaVersion != BoardSchemaVersion || !board.IsRunnableLegacyGraph() {
		t.Fatalf("migrated board is not a runnable v2 graph: version=%d runnable=%v", board.SchemaVersion, board.IsRunnableLegacyGraph())
	}
	if board.RunPolicy == nil || len(board.RunPolicy.Checks) != 1 || board.RunPolicy.Publish.Base != "main" || !board.RunPolicy.Publish.Draft {
		t.Fatalf("legacy verify/publish policy was lost: %+v", board.RunPolicy)
	}
	if got := board.ComponentByLegacyRole(ComponentReview); got == nil || got.Seat.Vendor != VendorCodex {
		t.Fatalf("legacy review seat was lost: %+v", got)
	}

	encoded := mustMarshal(t, board)
	again, err := DecodeBoard(encoded)
	if err != nil {
		t.Fatalf("decode migrated board: %v", err)
	}
	if !reflect.DeepEqual(board, again) {
		t.Fatalf("migration is not idempotent\nfirst:  %s\nsecond: %s", encoded, mustMarshal(t, again))
	}
}

func TestDecodeBoardRejectsForeignAndFutureSchemas(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"dagama","schemaVersion":2}`,
		`{"kind":"atlas","schemaVersion":99}`,
		`{"kind":"atlas","version":1}`,
	} {
		if _, err := DecodeBoard([]byte(raw)); err == nil {
			t.Fatalf("DecodeBoard(%s) unexpectedly succeeded", raw)
		}
	}
}

func TestAssertPolicyRejectsInvalidGraphsBeforeExecution(t *testing.T) {
	tests := map[string]func(*Board){
		"duplicate seat": func(board *Board) {
			board.Components[1].ID = board.Components[0].ID
		},
		"dangling edge": func(board *Board) {
			board.Edges[0].To = "missing"
		},
		"trigger cycle": func(board *Board) {
			board.Edges = append(board.Edges, Edge{ID: "edge-review-plan", From: "review", To: "plan", Kind: EdgeTrigger, Mode: TriggerAuto})
		},
		"unsafe check": func(board *Board) {
			board.RunPolicy = &RunPolicy{Checks: []Check{{Name: "unsafe", Argv: []string{"sh", "-c", "echo no"}}}}
		},
		"duplicate worker": func(board *Board) {
			board.Components[0].Seats = append(board.Components[0].Seats, board.Components[0].Seats[0])
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			board := DefaultBoard()
			mutate(board)
			var typed *Error
			if err := AssertPolicy(board); !errors.As(err, &typed) || typed.Code != CodePolicyViolation {
				t.Fatalf("AssertPolicy() error = %v, want %s", err, CodePolicyViolation)
			}
		})
	}
}

func TestV2GoldenRoundTripKeepsSemanticJSON(t *testing.T) {
	board, err := DecodeBoard(readFixture(t, "board-v2.json"))
	if err != nil {
		t.Fatalf("decode v2: %v", err)
	}
	encoded := mustMarshal(t, board)
	var first, second any
	if err := json.Unmarshal(encoded, &first); err != nil {
		t.Fatal(err)
	}
	again, err := DecodeBoard(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustMarshal(t, again), &second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("v2 semantic JSON changed after a second round trip")
	}
}
