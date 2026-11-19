package synthesis

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSourceQuoteSchemaBounds(t *testing.T) {
	var document any
	if err := json.Unmarshal([]byte(synthesisSchema), &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const name = "https://coslash.test/synthesis.schema.json"
	if err := compiler.AddResource(name, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(name)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, quote string
		valid       bool
	}{
		{"160 Unicode code points", strings.Repeat("界", 154) + " a b c", true},
		{"161 Unicode code points", strings.Repeat("界", 155) + " a b c", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := map[string]any{
				"goals": []any{"Ship"}, "outcome": "Done", "nextStep": "", "designDoc": "",
				"decisions": []any{map[string]any{"text": "Use bounded retry storage", "why": "Avoid duplication", "alternative": "", "byYou": false,
					"evidence": []any{map[string]any{"turn": 1, "quote": tt.quote}}}},
			}
			if err := schema.Validate(value); (err == nil) != tt.valid {
				t.Fatalf("valid=%t error=%v", tt.valid, err)
			}
		})
	}
}

func TestSourcePromptRestrictsCitationToVisibleTimeline(t *testing.T) {
	for _, required := range []string{
		"only from visible DIGEST or DIGEST CHUNK timeline description or Answer lines",
		"entire quote must appear before any truncation marker",
		"Never quote the GOAL CANDIDATES section, its First prompt or Declared goal headers",
		"first_prompt timeline row is eligible only for the words visible in that row",
	} {
		if !strings.Contains(systemPrompt, required) {
			t.Fatalf("prompt omitted citation boundary %q", required)
		}
	}
}
