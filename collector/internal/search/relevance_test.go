package search

import (
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestTitlePhraseOutranksScatteredTitleWithBodyBonus(t *testing.T) {
	i := openTest(t)
	review := fixture("review", "search design evaluation")
	review.Name = str("Review search feature design")
	review.DeclaredGoal = str("search design evaluation")
	review.LastActivityTime = 200
	implementation := fixture("implementation", "")
	implementation.Name = str("Implement local search design")
	implementation.LastActivityTime = 100
	for _, s := range []session.Session{review, implementation} {
		s.Repository = str("coslash")
		put(t, i, "local", s, "full")
	}
	for _, query := range []string{"search design", "why search design repo:coslash", `"search design"`} {
		r := find(t, i, query, Options{Repo: "coslash"})
		if len(r.Sessions) == 0 || r.Sessions[0].SessionID != "implementation" {
			t.Fatalf("%q: %+v", query, r.Sessions)
		}
	}
	// One-word queries retain field weights without a phrase boost.
	r := find(t, i, "search", Options{})
	if r.Sessions[0].SessionID != "review" {
		t.Fatalf("single word ranking: %+v", r.Sessions)
	}
}

func TestDecisionCardsRequireEveryTermInSameUnit(t *testing.T) {
	i := openTest(t)
	s := fixture("partial", "search design")
	s.Synthesis = &session.SessionSynthesis{DecisionsVersion: "v1", Decisions: []session.Decision{{ID: "search-only", Text: "Use search", Cited: true}, {ID: "design-only", Text: "Keep design", Cited: true}}}
	put(t, i, "local", s, "full")
	r := find(t, i, "search design", Options{})
	if len(r.Sessions) != 1 || len(r.Decisions) != 0 {
		t.Fatalf("session-wide coverage admitted partial cards: %+v", r)
	}
	r = find(t, i, "search", Options{})
	if len(r.Decisions) != 1 || r.Decisions[0].DecisionID != "search-only" {
		t.Fatalf("single term: %+v", r)
	}
	// A partial decision remains a valid session match even without matching body text.
	s.Digest = nil
	s.DetailRevision = "r2"
	put(t, i, "local", s, "full")
	r = find(t, i, "search missingword", Options{})
	if len(r.Sessions) != 1 || r.Sessions[0].Match.Kind != "decision" || len(r.Decisions) != 0 {
		t.Fatalf("partial session retrieval: %+v", r)
	}
}

func TestDecisionCardTermsUseFTSPhrasesStemmingAndRationale(t *testing.T) {
	i := openTest(t)
	s := fixture("rationale", "")
	s.Synthesis = &session.SessionSynthesis{DecisionsVersion: "v1", Decisions: []session.Decision{
		{ID: "rationale", Text: "Promoted artifacts", Why: "Search design preserves ancestry", Alternative: "Squash merging", Cited: true},
		{ID: "scattered", Text: "Promoted artifacts", Why: "Search feature design preserves ancestry", Cited: true},
	}}
	put(t, i, "local", s, "full")
	for _, test := range []struct {
		query string
		ids   []string
	}{
		{`promote "search design"`, []string{"rationale"}},
		{"promote ancestry", []string{"rationale", "scattered"}},
		{"promote squash", []string{"rationale"}},
		{`"ancestry promoted"`, nil},
	} {
		r := find(t, i, test.query, Options{})
		got := map[string]bool{}
		want := map[string]bool{}
		for _, d := range r.Decisions {
			got[d.DecisionID] = true
		}
		for _, id := range test.ids {
			want[id] = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%q cards %v want %v", test.query, got, want)
		}
	}
}

func TestDecisionEligibilityPrecedesPerSessionCapAndFilters(t *testing.T) {
	i := openTest(t)
	for _, repo := range []string{"excluded", "included"} {
		s := fixture(repo, "search design")
		s.Repository = &repo
		s.LastActivityTime = 100
		s.Synthesis = &session.SessionSynthesis{DecisionsVersion: "v1", Decisions: []session.Decision{
			{ID: "partial-one", Text: "search", Cited: true},
			{ID: "partial-two", Text: "search", Cited: true},
			{ID: "complete", Text: "Search design uses bounded keyword matching", Cited: true},
		}}
		put(t, i, "local", s, "full")
	}
	r := find(t, i, "search design", Options{Repo: "included", Agent: "claude", Since: 100, Limit: 1})
	if len(r.Sessions) != 1 || r.Sessions[0].SessionID != "included" || len(r.Decisions) != 1 || r.Decisions[0].DecisionID != "complete" {
		t.Fatalf("filtered cards: %+v", r)
	}
	r = find(t, i, "search design", Options{Since: 101})
	if len(r.Sessions) != 0 || len(r.Decisions) != 0 {
		t.Fatalf("since filter: %+v", r)
	}
}
