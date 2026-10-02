package fullsessionv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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

func TestFreezeGeneratedChangeIDsSkipSuppliedIDs(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			StartedAtMs: 1, LastActivityAtMs: 1, EditedFileCount: 2,
			FileEdits: []FileEdit{
				{Path: "first.go", Edits: 1, Changes: []FileChange{{Kind: "content", Text: "first", Operation: "Write"}}},
				{Path: "second.go", Edits: 1, Changes: []FileChange{{ID: "change-000000-000000", Kind: "content", Text: "second", Operation: "Write"}}},
			},
		},
	}

	frozen, err := Freeze(record)
	if err != nil {
		t.Fatal(err)
	}
	generated := frozen.Session.FileEdits[0].Changes[0].ID
	if generated != "change-000000-000000-000001" {
		t.Fatalf("generated ID = %q, want collision-free deterministic ID", generated)
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

func TestFreezeRejectsCostAboveExactAdapterLimit(t *testing.T) {
	record := Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Usage: []ModelUsage{{
			Model: "gpt-5", CostMicroUSD: MaxCostMicroUSD + 1,
		}}},
	}
	if _, err := Freeze(record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze error = %v; want %v", err, ErrInvalid)
	}
}

func TestValidateRejectsOversizedRecord(t *testing.T) {
	commands := make([]string, MaxItems)
	command := strings.Repeat("x", MaxRecordBytes/MaxItems+1)
	for index := range commands {
		commands[index] = command
	}
	record := Record{
		SchemaVersion: SchemaVersion, SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Commands: commands},
	}
	preimage, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(preimage)
	record.RevisionID = hex.EncodeToString(digest[:])

	if err := Validate(record); !errors.Is(err, ErrOversized) {
		t.Fatalf("Validate error = %v; want %v", err, ErrOversized)
	}
}

func TestValidationErrorPrecedence(t *testing.T) {
	commands := make([]string, 11)
	for index := range commands {
		commands[index] = strings.Repeat("<", MaxStringBytes)
	}
	for _, test := range []struct {
		name       string
		revision   string
		commands   []string
		lastActive int64
		want       error
	}{
		{"revision syntax before size", "bad", commands, 1, fmt.Errorf("%w: invalid revision", ErrInvalid)},
		{"session validity before size", strings.Repeat("0", 64), commands, 0, fmt.Errorf("%w: invalid session counts or time", ErrInvalid)},
		{"size before revision hash", strings.Repeat("0", 64), commands, 1, ErrOversized},
		{"bounded revision hash mismatch", strings.Repeat("0", 64), nil, 1, fmt.Errorf("%w: revision hash mismatch", ErrInvalid)},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := Record{
				SchemaVersion: SchemaVersion, SourceID: "source-1", Agent: "codex", SessionID: "session-1",
				RevisionID: test.revision,
				Session:    Session{StartedAtMs: 1, LastActivityAtMs: test.lastActive, Commands: test.commands},
			}
			var input bytes.Buffer
			encoder := json.NewEncoder(&input)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(record); err != nil {
				t.Fatal(err)
			}
			if input.Len() > MaxRecordBytes {
				t.Fatal("unescaped input exceeds byte limit before validation")
			}
			wantKind := ErrInvalid
			if test.want == ErrOversized {
				wantKind = ErrOversized
			}
			for _, check := range []struct {
				name string
				run  func() error
			}{
				{"Validate", func() error { return Validate(record) }},
				{"Marshal", func() error {
					data, err := Marshal(record)
					if data != nil {
						t.Fatal("Marshal returned bytes for an invalid record")
					}
					return err
				}},
				{"Decode", func() error {
					decoded, err := Decode(bytes.TrimSuffix(input.Bytes(), []byte("\n")))
					if !reflect.DeepEqual(decoded, Record{}) {
						t.Fatal("Decode returned a record for invalid input")
					}
					return err
				}},
			} {
				if err := check.run(); err == nil || !errors.Is(err, wantKind) || err.Error() != test.want.Error() {
					t.Fatalf("%s error = %v; want %v", check.name, err, test.want)
				}
			}
		})
	}
}

func TestDecodeGateErrorPrecedence(t *testing.T) {
	record := Record{
		SchemaVersion: SchemaVersion, SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		RevisionID: "bad", Session: Session{StartedAtMs: 1, LastActivityAtMs: 1},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte(`{"unknown":null,`), data[1:]...)
	collection := []byte(`{"unknown":[` + strings.Repeat("{},", MaxItems) + `{}],"revisionId":"bad"}`)
	for _, test := range []struct {
		name string
		data []byte
		want string
	}{
		{"input size before preflight", bytes.Repeat([]byte(" "), MaxRecordBytes+1), ErrOversized.Error()},
		{"collection preflight before unknown fields", collection, ErrInvalid.Error() + ": collection exceeds item limit"},
		{"unknown fields before EOF and validation", append(unknown, []byte(" {}")...), ErrInvalid.Error() + `: decode: json: unknown field "unknown"`},
		{"EOF before validation", append(append([]byte(nil), data...), []byte(" {}")...), ErrInvalid.Error() + ": trailing JSON value"},
		{"validation before canonical comparison", append([]byte(" "), data...), ErrInvalid.Error() + ": invalid revision"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(test.data); err == nil || err.Error() != test.want {
				t.Fatalf("Decode error = %v; want %s", err, test.want)
			}
		})
	}
}

func TestMarshalEncodedByteLimit(t *testing.T) {
	record := Record{
		SchemaVersion: SchemaVersion, SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		RevisionID: strings.Repeat("0", 64),
		Session:    Session{StartedAtMs: 1, LastActivityAtMs: 1, Commands: make([]string, 11)},
	}
	base, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	remaining := MaxRecordBytes - len(base)
	for index := range record.Session.Commands {
		count := min(remaining/6, MaxStringBytes)
		record.Session.Commands[index] = strings.Repeat("<", count)
		remaining -= count * 6
	}
	last := len(record.Session.Commands) - 1
	record.Session.Commands[last] += strings.Repeat("x", remaining)
	record, err = Freeze(record)
	if err != nil {
		t.Fatalf("Freeze at encoded byte limit: %v", err)
	}
	data, err := Marshal(record)
	if err != nil || len(data) != MaxRecordBytes {
		t.Fatalf("Marshal at encoded byte limit: bytes = %d, error = %v", len(data), err)
	}
	if err := Validate(record); err != nil {
		t.Fatalf("Validate at encoded byte limit: %v", err)
	}
	record.Session.Commands[last] += "x"
	if data, err := Marshal(record); data != nil || !errors.Is(err, ErrOversized) {
		t.Fatalf("Marshal one byte over encoded limit: bytes = %d, error = %v", len(data), err)
	}
}

func TestMarshalAndDecodeStorageOwnership(t *testing.T) {
	name := "original"
	record, err := Freeze(Record{
		SourceID: "source-1", Agent: "codex", SessionID: "session-1",
		Session: Session{
			Name: &name, StartedAtMs: 1, LastActivityAtMs: 1,
			Commands: []string{"original command"}, Usage: []ModelUsage{},
			EditedFileCount: 1, FileEdits: []FileEdit{{
				Path: "main.go", Edits: 1,
				Changes: []FileChange{{Kind: "content", Text: "body", Operation: "Write"}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := cloneRecord(record)
	if err := Validate(record); err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	canonical := bytes.Clone(data)
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record, want) || !reflect.DeepEqual(decoded, want) || !bytes.Equal(data, canonical) {
		t.Fatal("Validate, Marshal or Decode mutated input or changed null/empty distinctions")
	}
	clear(data)
	if !reflect.DeepEqual(decoded, want) {
		t.Fatal("decoded record aliases input bytes")
	}
	fresh, err := Marshal(record)
	if err != nil || !bytes.Equal(fresh, canonical) {
		t.Fatalf("Marshal reused caller-owned bytes: %v", err)
	}
	second, err := Decode(fresh)
	if err != nil {
		t.Fatal(err)
	}
	*decoded.Session.Name = "changed"
	decoded.Session.Commands[0] = "changed command"
	decoded.Session.FileEdits[0].Changes[0].Text = "changed body"
	if !reflect.DeepEqual(record, want) || !reflect.DeepEqual(second, want) {
		t.Fatal("decoded record aliases original record or another Decode call")
	}
	*record.Session.Name = "changed original"
	if !bytes.Equal(fresh, canonical) || *decoded.Session.Name != "changed" {
		t.Fatal("caller mutation changes encoded bytes or decoded storage")
	}
}
