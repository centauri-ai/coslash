package sessionbackupv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type ecmaRegexp regexp2.Regexp

func (expression *ecmaRegexp) MatchString(value string) bool {
	matched, err := (*regexp2.Regexp)(expression).MatchString(value)
	return err == nil && matched
}

func (expression *ecmaRegexp) String() string {
	return (*regexp2.Regexp)(expression).String()
}

func compileECMA(expression string) (jsonschema.Regexp, error) {
	compiled, err := regexp2.Compile(expression, regexp2.ECMAScript)
	return (*ecmaRegexp)(compiled), err
}

func TestPublishedSchemasAreValidJSON(t *testing.T) {
	for _, name := range []string{"schema.json", "database-rows.schema.json", "enrichment.schema.json", "synthesis.schema.json"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(data) {
			t.Fatalf("%s is not valid JSON", name)
		}
	}
}

func TestValidFixtureSatisfiesContractAndSchemas(t *testing.T) {
	manifest, manifestBytes, blobs := loadValidFixture(t)
	validateAgainstSchema(t, "schema.json", manifestBytes)
	if !bytes.Contains(manifestBytes, []byte(`"captureProblems":[]`)) || bytes.Contains(manifestBytes, []byte(`"captureProblems":null`)) {
		t.Fatalf("manifest does not canonically encode an empty capture problem array: %s", manifestBytes)
	}
	for _, artifact := range manifest.Artifacts {
		switch artifact.Kind {
		case KindSessionEnrichment:
			validateAgainstSchema(t, "enrichment.schema.json", blobs[artifact.LogicalName])
			if _, err := DecodeEnrichment(blobs[artifact.LogicalName]); err != nil {
				t.Fatal(err)
			}
		case KindSynthesis:
			validateAgainstSchema(t, "synthesis.schema.json", blobs[artifact.LogicalName])
			if _, err := DecodeSynthesisRecord(blobs[artifact.LogicalName]); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestManifestSchemaRejectsNullProblemsAndRequiredVersionDrift(t *testing.T) {
	_, manifestBytes, _ := loadValidFixture(t)
	var document map[string]any
	if err := json.Unmarshal(manifestBytes, &document); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{
		"null capture problems":      nil,
		"unsorted required versions": []any{SchemaVersion, ParsedRecordVersion},
		"missing manifest version":   []any{ParsedRecordVersion},
		"missing parsed version":     []any{SchemaVersion},
		"unsupported database rows":  []any{ParsedRecordVersion, DatabaseRowsVersion, SchemaVersion},
	} {
		name, value := name, value
		t.Run(name, func(t *testing.T) {
			candidate := make(map[string]any, len(document))
			for key, original := range document {
				candidate[key] = original
			}
			if strings.Contains(name, "capture") {
				candidate["captureProblems"] = value
			} else {
				candidate["requiredVersions"] = value
			}
			data, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := schemaValidationError(t, "schema.json", data); err == nil {
				t.Fatal("schema accepted invalid manifest shape")
			}
		})
	}
}

func TestFreezeRequiresCoreArtifactsForEveryMember(t *testing.T) {
	for _, kind := range []string{KindParsedSessionRecord, KindRawTranscript} {
		t.Run(kind, func(t *testing.T) {
			manifest, _, blobs := loadValidFixture(t)
			childID := manifest.Members[1].MemberID
			artifacts := manifest.Artifacts[:0]
			for _, artifact := range manifest.Artifacts {
				if artifact.MemberID == childID && artifact.Kind == kind {
					delete(blobs, artifact.LogicalName)
					continue
				}
				artifacts = append(artifacts, artifact)
			}
			manifest.Artifacts = artifacts
			if _, err := Freeze(manifest, blobs); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("Freeze() error = %v; want incomplete", err)
			}
		})
	}
}

func TestFreezeBindsTypedArtifactsToMembers(t *testing.T) {
	manifest, _, blobs := loadValidFixture(t)
	var records []string
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind == KindParsedSessionRecord {
			records = append(records, artifact.LogicalName)
		}
	}
	blobs[records[0]], blobs[records[1]] = blobs[records[1]], blobs[records[0]]
	if _, err := Freeze(manifest, blobs); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze() error = %v; want invalid", err)
	}
}

func TestFreezeBindsSynthesisToMemberRevision(t *testing.T) {
	manifest, _, blobs := loadValidFixture(t)
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind != KindSynthesis {
			continue
		}
		record, err := DecodeSynthesisRecord(blobs[artifact.LogicalName])
		if err != nil {
			t.Fatal(err)
		}
		record.SessionID = manifest.Members[1].MemberID
		blobs[artifact.LogicalName], err = MarshalSynthesisRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if _, err := Freeze(manifest, blobs); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze() error = %v; want invalid", err)
	}
}

func TestFreezeBindsExactChangeBodies(t *testing.T) {
	manifest, _, blobs := loadValidFixture(t)
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind == KindExactChangeBody {
			blobs[artifact.LogicalName] = []byte("different body")
			break
		}
	}
	if _, err := Freeze(manifest, blobs); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Freeze() error = %v; want invalid", err)
	}
}

func TestValidateRejectsNilProblemsAndNonCanonicalMembers(t *testing.T) {
	manifest, _, _ := loadValidFixture(t)
	manifest.CaptureProblems = nil
	if err := Validate(manifest); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Validate() error = %v; want incomplete", err)
	}

	manifest, _, _ = loadValidFixture(t)
	manifest.Members[1].Ordinal = 2
	if err := Validate(manifest); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v; want invalid", err)
	}
}

func TestFreezeDatabaseRowsDoesNotMutateInput(t *testing.T) {
	rows := DatabaseRows{Database: "cache", Tables: []DatabaseTable{
		{Name: "z", Columns: []string{"value"}, Rows: []DatabaseRow{
			{MemberID: "b", Values: []DatabaseValue{{Type: "text", Value: "b"}}},
			{MemberID: "a", Values: []DatabaseValue{{Type: "text", Value: "a"}}},
		}},
		{Name: "a", Columns: []string{"value"}, Rows: []DatabaseRow{
			{MemberID: "a", Values: []DatabaseValue{{Type: "text", Value: "a"}}},
		}},
	}}
	before, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FreezeDatabaseRows(rows); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("FreezeDatabaseRows mutated caller-owned rows")
	}
}

func TestDocumentDecodersRejectAggregateItemOverflow(t *testing.T) {
	data := []byte(`{"agent":"codex","sessionId":"session","mtime":1,"model":"model","generatedAt":1,"synthesis":{"goals":[` +
		strings.Repeat(`"",`, fullsessionv1.MaxItems) + `""],"outcome":"","keyDecisions":[],"nextStep":""}}`)
	if _, err := DecodeSynthesisRecord(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DecodeSynthesisRecord() error = %v; want invalid", err)
	}
}

func TestDatabaseValueSchemaMatchesDecoder(t *testing.T) {
	invalid := []struct {
		name  string
		value string
	}{
		{"null payload", `{"type":"null","value":"x"}`},
		{"noncanonical int", `{"type":"integer","value":"01"}`},
		{"integer overflow", `{"type":"integer","value":"9223372036854775808"}`},
		{"invalid real bits", `{"type":"real","value":"not-bits"}`},
		{"noncanonical base64", `{"type":"blob","value":"AB=="}`},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			data := databaseRowsWithValue(test.value)
			if err := schemaValidationError(t, "database-rows.schema.json", data); err == nil {
				t.Fatal("schema accepted invalid database value")
			}
			if _, err := DecodeDatabaseRows(data, "member"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("DecodeDatabaseRows() error = %v; want invalid", err)
			}
		})
	}

	valid := []string{
		`{"type":"null","value":""}`,
		`{"type":"integer","value":"-9223372036854775808"}`,
		`{"type":"integer","value":"9223372036854775807"}`,
		`{"type":"real","value":"0000000000000000"}`,
		`{"type":"text","value":"value"}`,
		`{"type":"blob","value":"AA=="}`,
	}
	for _, value := range valid {
		data := databaseRowsWithValue(value)
		validateAgainstSchema(t, "database-rows.schema.json", data)
		if _, err := DecodeDatabaseRows(data, "member"); err != nil {
			t.Fatalf("DecodeDatabaseRows(%s): %v", value, err)
		}
	}
}

func databaseRowsWithValue(value string) []byte {
	return []byte(`{"schemaVersion":"session-backup-db-rows/v1","database":"cache","tables":[{"name":"records","columns":["value"],"rows":[{"memberId":"member","values":[` + value + `]}]}]}`)
}

func TestFreezeIsDeterministicAcrossInputOrder(t *testing.T) {
	manifest, want, blobs := loadValidFixture(t)
	manifest.CompleteBackupSHA256 = ""
	slices.Reverse(manifest.RequiredVersions)
	slices.Reverse(manifest.Members)
	slices.Reverse(manifest.Artifacts)
	gotManifest, err := Freeze(manifest, blobs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(gotManifest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("canonical manifest changed with input order")
	}
}

func TestDatabaseRowsRejectCrossSessionAttribution(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "database-rows", "invalid-cross-session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeDatabaseRows(data, "member-b"); err == nil {
		t.Fatal("cross-session database row accepted")
	}
}

func TestLogicalNameSchemaMatchesContract(t *testing.T) {
	manifest, _, _ := loadValidFixture(t)
	for _, name := range []string{"foo/./bar", "foo/../bar", "foo//bar", "foo/bar/", "/foo", `foo\\bar`, "C:/foo", "foo\x1fbar"} {
		if logicalName(name) {
			t.Fatalf("logicalName(%q) accepted", name)
		}
		candidate := cloneManifest(manifest)
		candidate.Artifacts[0].LogicalName = name
		data, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := schemaValidationError(t, "schema.json", data); err == nil {
			t.Fatalf("schema accepted logical name %q", name)
		}
	}
}

func TestCaptureProblemBlocksFreeze(t *testing.T) {
	manifest, _, blobs := loadValidFixture(t)
	manifest.CaptureProblems = []CaptureProblem{{Code: ProblemUnreadable, MemberID: manifest.Family.RootMemberID, Kind: KindRawTranscript, Retryable: true}}
	if _, err := Freeze(manifest, blobs); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("freeze error = %v; want incomplete", err)
	}
}

func loadValidFixture(t *testing.T) (Manifest, []byte, map[string][]byte) {
	t.Helper()
	root := filepath.Join("testdata", "fixtures", "valid", "family")
	manifestBytes, err := os.ReadFile(filepath.Join(root, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := Validate(manifest); err != nil {
		t.Fatal(err)
	}
	blobs := map[string][]byte{}
	for _, artifact := range manifest.Artifacts {
		blob, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		blobs[artifact.LogicalName] = blob
	}
	return manifest, manifestBytes, blobs
}

func validateAgainstSchema(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := schemaValidationError(t, name, data); err != nil {
		t.Fatalf("%s validation failed: %v", name, err)
	}
}

func schemaValidationError(t *testing.T, name string, data []byte) error {
	t.Helper()
	schemaBytes, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaBytes, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseRegexpEngine(compileECMA)
	if err := compiler.AddResource(name, schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(name)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return schema.Validate(value)
}
