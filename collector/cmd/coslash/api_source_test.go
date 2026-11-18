package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

// The frontend shows shareEligibility as sent and does not re-derive it.
func TestEligibilityForOrdersReasons(t *testing.T) {
	cost := 1.0
	running := "running"
	cases := []struct {
		name           string
		value          session.Session
		sourceComplete bool
		stale          bool
		want           string
	}{
		{"private wins over every other reason", session.Session{RepositoryLocalOnly: true, Status: &running}, false, true, "private"},
		{"stale wins over incomplete", session.Session{Cost: &cost}, false, true, "stale"},
		{"incomplete source", session.Session{Cost: &cost}, false, false, "incomplete"},
		{"running", session.Session{Status: &running, Cost: &cost}, true, false, "running"},
		{"no cost", session.Session{}, true, false, "failed"},
		{"eligible", session.Session{Cost: &cost}, true, false, "eligible"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := test.value
			if got := eligibilityFor(&value, test.sourceComplete, test.stale); got != test.want {
				t.Fatalf("eligibilityFor = %q, want %q", got, test.want)
			}
		})
	}
}

// The frontend decodes collections without null checks.
func TestSessionWithJSONCollectionsSendsNoNullCollections(t *testing.T) {
	value := session.Session{Subagents: []session.Subagent{{}}}
	value.Synthesis = &session.SessionSynthesis{}
	data, err := json.Marshal(sessionWithJSONCollections(value))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tokens", "observedModels", "unpricedModels", "subagents", "commands", "commits", "todos", "digest", "fileEdits", "goals", "keyDecisions"} {
		if strings.Contains(string(data), `"`+key+`":null`) {
			t.Errorf("%s is null in %s", key, data)
		}
	}
}
