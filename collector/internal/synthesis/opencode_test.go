package synthesis

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseOpenCodeSynthesisRejectsIncompleteObject(t *testing.T) {
	cases := map[string]string{
		"missing every other field": `{"outcome":"done"}`,
		"missing nextStep":          `{"goals":["ship"],"outcome":"done","keyDecisions":[]}`,
		"empty goals":               `{"goals":[],"outcome":"done","keyDecisions":[],"nextStep":"land it"}`,
		"null goals":                `{"goals":null,"outcome":"done","keyDecisions":[],"nextStep":"land it"}`,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOpenCodeSynthesis(text); err == nil {
				t.Fatalf("parseOpenCodeSynthesis(%s) succeeded, want an error", text)
			}
		})
	}
}

func TestParseOpenCodeSynthesisAcceptsCompleteObject(t *testing.T) {
	cases := map[string]string{
		"bare object":  `{"goals":["ship the fix"],"outcome":"done","keyDecisions":[],"nextStep":"land it"}`,
		"fenced":       "```json\n{\"goals\":[\"ship the fix\"],\"outcome\":\"done\",\"keyDecisions\":[],\"nextStep\":\"land it\"}\n```",
		"around prose": `Here you go: {"goals":["ship the fix"],"outcome":"done","keyDecisions":[],"nextStep":"land it"} hope that helps.`,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			synthesis, err := parseOpenCodeSynthesis(text)
			if err != nil {
				t.Fatalf("parseOpenCodeSynthesis(%s): %v", text, err)
			}
			if len(synthesis.Goals) != 1 || synthesis.Goals[0] != "ship the fix" {
				t.Fatalf("goals = %v, want [ship the fix]", synthesis.Goals)
			}
			if synthesis.NextStep != "land it" {
				t.Fatalf("nextStep = %q, want %q", synthesis.NextStep, "land it")
			}
		})
	}
}

func TestExtractJSONObject(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"nested objects", `x {"a":{"b":{"c":1}}} y`, `{"a":{"b":{"c":1}}}`},
		{"brace inside string", `{"a":"} not the end","b":1}`, `{"a":"} not the end","b":1}`},
		{"escaped quote before brace", `{"a":"say \"}\" now","b":2}`, `{"a":"say \"}\" now","b":2}`},
		{"stops after first value", `{"a":1} {"b":2}`, `{"a":1}`},
		{"no object", `just prose`, ``},
		{"unterminated", `{"a":1`, ``},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := extractJSONObject(test.input); got != test.want {
				t.Fatalf("extractJSONObject(%s) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestCleanupOpenCodeScratchRemovesOnlyAbandonedDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	root := SynthesisCwd()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(root, openCodeScratchPrefix+"stale")
	live := filepath.Join(root, openCodeScratchPrefix+"live")
	unrelated := filepath.Join(root, "summaries-ish")
	for _, directory := range []string{stale, live, unrelated} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * OpenCodeScratchMaxAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unrelated, old, old); err != nil {
		t.Fatal(err)
	}

	if err := CleanupOpenCodeScratch(); err != nil {
		t.Fatalf("CleanupOpenCodeScratch: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("abandoned scratch directory survived the sweep")
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("in-flight scratch directory was swept: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated directory was swept: %v", err)
	}
}
