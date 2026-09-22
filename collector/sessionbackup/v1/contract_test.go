package sessionbackupv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

func TestValidateRejectsNilProblems(t *testing.T) {
	manifest, _, _ := loadValidFixture(t)
	manifest.CaptureProblems = nil
	if err := Validate(manifest); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Validate() error = %v; want incomplete", err)
	}
}

func TestValidateRejectsNonCanonicalSiblingOrder(t *testing.T) {
	manifest, _, _ := loadValidFixture(t)
	childID := manifest.Members[1].MemberID
	sibling := manifest.Members[1]
	sibling.MemberID = childID + "-sibling"
	sibling.SourceRevision = "sibling-source-revision"
	manifest.Members = []Member{manifest.Members[0], sibling, manifest.Members[1]}
	for index := range manifest.Members {
		manifest.Members[index].Ordinal = index
	}

	artifacts := make([]Artifact, 0, len(manifest.Artifacts)*2)
	for _, artifact := range manifest.Artifacts {
		if artifact.MemberID == manifest.Family.RootMemberID {
			artifacts = append(artifacts, artifact)
		}
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.MemberID != childID {
			continue
		}
		copy := artifact
		copy.MemberID = sibling.MemberID
		copy.LogicalName = strings.Replace(copy.LogicalName, "members/child/", "members/sibling/", 1)
		artifacts = append(artifacts, copy)
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.MemberID == childID {
			artifacts = append(artifacts, artifact)
		}
	}
	for index := range artifacts {
		artifacts[index].Ordinal = index
	}
	manifest.Artifacts = artifacts
	manifest.Summary = summarize(artifacts)
	manifest.CompleteBackupSHA256 = ""
	preimage, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(preimage)
	manifest.CompleteBackupSHA256 = hex.EncodeToString(digest[:])

	if err := Validate(manifest); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "members are not deterministically ordered") {
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

func TestDocumentProducersEnforceDecoderBounds(t *testing.T) {
	t.Run("synthesis aggregate", func(t *testing.T) {
		record := SynthesisRecord{
			Agent: "codex", SessionID: "session", Revision: 1, Model: "model", GeneratedAt: 1,
			Synthesis: fullsessionv1.SessionSynthesis{
				Goals: make([]string, fullsessionv1.MaxItems-1), KeyDecisions: []string{"decision"},
			},
		}
		data, err := MarshalSynthesisRecord(record)
		if err != nil {
			t.Fatalf("MarshalSynthesisRecord() at item limit: %v", err)
		}
		if _, err := DecodeSynthesisRecord(data); err != nil {
			t.Fatalf("DecodeSynthesisRecord() at item limit: %v", err)
		}
		record.Synthesis.KeyDecisions = append(record.Synthesis.KeyDecisions, "overflow")
		if _, err := MarshalSynthesisRecord(record); !errors.Is(err, ErrInvalid) {
			t.Fatalf("MarshalSynthesisRecord() error = %v; want invalid", err)
		}
	})

	t.Run("synthesis bytes", func(t *testing.T) {
		item := strings.Repeat("x", fullsessionv1.MaxStringBytes)
		record := SynthesisRecord{
			Agent: "codex", SessionID: "session", Revision: 1, Model: "model", GeneratedAt: 1,
			Synthesis: fullsessionv1.SessionSynthesis{Goals: make([]string, fullsessionv1.MaxRecordBytes/fullsessionv1.MaxStringBytes)},
		}
		for index := range record.Synthesis.Goals {
			record.Synthesis.Goals[index] = item
		}
		if _, err := MarshalSynthesisRecord(record); !errors.Is(err, ErrInvalid) {
			t.Fatalf("MarshalSynthesisRecord() error = %v; want invalid", err)
		}
	})

	t.Run("database rows aggregate", func(t *testing.T) {
		rows := databaseRowsForItemLimit(fullsessionv1.MaxItems/2 - 1)
		data, err := FreezeDatabaseRows(rows)
		if err != nil {
			t.Fatalf("FreezeDatabaseRows() at item limit: %v", err)
		}
		if _, err := DecodeDatabaseRows(data, "member"); err != nil {
			t.Fatalf("DecodeDatabaseRows() at item limit: %v", err)
		}
		rows.Tables[0].Rows = append(rows.Tables[0].Rows, DatabaseRow{
			MemberID: "member", Values: []DatabaseValue{{Type: "text", Value: "overflow"}},
		})
		if _, err := FreezeDatabaseRows(rows); !errors.Is(err, ErrInvalid) {
			t.Fatalf("FreezeDatabaseRows() error = %v; want invalid", err)
		}
	})
}

func databaseRowsForItemLimit(rowCount int) DatabaseRows {
	rows := make([]DatabaseRow, rowCount)
	for index := range rows {
		rows[index] = DatabaseRow{
			MemberID: "member",
			Values:   []DatabaseValue{{Type: "text", Value: fmt.Sprintf("%05d", index)}},
		}
	}
	return DatabaseRows{Database: "cache", Tables: []DatabaseTable{{
		Name: "records", Columns: []string{"value"}, Rows: rows,
	}}}
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
