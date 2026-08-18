package synthesis

import "testing"

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
