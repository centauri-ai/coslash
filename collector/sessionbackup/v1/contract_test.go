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
