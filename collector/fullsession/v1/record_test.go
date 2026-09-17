package fullsessionv1

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFreezeDoesNotMutateOrAliasInput(t *testing.T) {
	name, model := "original name", "original model"
	duration, turn := 10, 2
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			Name: &name, EditedFileCount: 1, StartedAtMs: 1, LastActivityAtMs: 2,
			Usage: []ModelUsage{}, Commands: []string{"original command"},
			Subagents: []Subagent{{
				ID: "child-1", Name: "child", Model: &model, Status: "returned",
				DurationMs: &duration, SpawnedAtTurn: &turn,
				Commands: []SubagentCommand{{Label: "check", Command: "go test ./..."}},
				Usage:    []ModelUsage{},
			}},
			FileEdits: []FileEdit{{
				Path: "main.go", Edits: 1,
				Changes: []FileChange{{Kind: "content", Text: "original body", Operation: "Write"}},
			}},
			Synthesis: &SessionSynthesis{Goals: []string{"original goal"}, KeyDecisions: []string{}},
		},
	}

	frozen, err := Freeze(record)
	if err != nil {
		t.Fatal(err)
	}
	if record.Session.FileEdits[0].Changes[0].ID != "" ||
		record.Session.FileEdits[0].Changes[0].ByteCount != 0 ||
		record.Session.FileEdits[0].Changes[0].SHA256 != "" {
		t.Fatalf("Freeze mutated input change: %#v", record.Session.FileEdits[0].Changes[0])
	}

	*record.Session.Name = "changed name"
	record.Session.Commands[0] = "changed command"
	*record.Session.Subagents[0].Model = "changed model"
	record.Session.Subagents[0].Commands[0].Command = "changed subagent command"
	record.Session.FileEdits[0].Changes[0].Text = "changed body"
	record.Session.Synthesis.Goals[0] = "changed goal"

	if *frozen.Session.Name != "original name" || frozen.Session.Commands[0] != "original command" ||
		*frozen.Session.Subagents[0].Model != "original model" ||
		frozen.Session.Subagents[0].Commands[0].Command != "go test ./..." ||
		frozen.Session.FileEdits[0].Changes[0].Text != "original body" ||
		frozen.Session.Synthesis.Goals[0] != "original goal" {
		t.Fatalf("frozen record aliases caller storage: %#v", frozen.Session)
	}
	if err := Validate(frozen); err != nil {
		t.Fatalf("caller mutation invalidated frozen record: %v", err)
	}
}

func TestFreezeValidationFailureDoesNotMutateInput(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			EditedFileCount: 1,
			FileEdits: []FileEdit{{Path: "main.go", Edits: 1, Changes: []FileChange{{
				Kind: "content", Text: "body", Operation: "Write",
			}}}},
		},
	}
	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
	change := record.Session.FileEdits[0].Changes[0]
	if change.ID != "" || change.ByteCount != 0 || change.SHA256 != "" {
		t.Fatalf("failed Freeze mutated input change: %#v", change)
	}
}

func TestDecodeRejectsOversizedCollectionBeforeTypedDecode(t *testing.T) {
	data := make([]byte, 0, 3*MaxItems+4)
	data = append(data, '[')
	data = append(data, bytes.Repeat([]byte("{},"), MaxItems)...)
	data = append(data, '{', '}', ']')

	if _, err := Decode(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Decode error = %v; want %v", err, ErrInvalid)
	}
}

func TestDecodeRejectsOversizedAggregateCollectionBeforeTypedDecode(t *testing.T) {
	items := strings.Repeat("{},", MaxItems/2-1) + "{}"
	data := []byte("[[" + items + "],[" + items + "]]")

	if err := validateCollectionSizes(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateCollectionSizes error = %v; want %v", err, ErrInvalid)
	}
}

func TestFreezeRejectsOversizedAggregateCollection(t *testing.T) {
	usage := make([]ModelUsage, MaxItems)
	for index := range usage {
		usage[index].Model = fmt.Sprintf("model-%06d", index)
	}
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			StartedAtMs: 1, LastActivityAtMs: 1,
			Usage: usage, Commands: []string{"go test ./..."},
		},
	}

	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
}

func TestFreezeRejectsUnsortedUsage(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Usage: []ModelUsage{
			{Model: "z-model"}, {Model: "a-model"},
		}},
	}

	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
}

func TestFreezeRejectsUnsortedSubagentUsage(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Subagents: []Subagent{{
			ID: "subagent-1", Usage: []ModelUsage{{Model: "z-model"}, {Model: "a-model"}},
		}}},
	}

	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
}
