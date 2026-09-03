package session

import (
	"strings"
	"testing"
)

func TestReconcileCommitFactsExportsOnlyResolvedFullObjectIDs(t *testing.T) {
	full := strings.Repeat("a", 40)
	facts := reconcileCommitFacts(
		[]CommitObservation{{Hash: full[:12], Subject: "ship it"}},
		[]repositoryCommit{{hash: full, subject: "ship it"}}, true,
	)
	if got := facts.Subjects; len(got) != 1 || got[0] != "ship it" {
		t.Fatalf("subjects = %#v", got)
	}
	if got := facts.SHAs; len(got) != 1 || got[0] != full {
		t.Fatalf("SHAs = %#v", got)
	}

	// A subject can still be retained for people when the observed abbreviated
	// hash cannot be resolved. It must never become a guessed Git identifier.
	facts = reconcileCommitFacts(
		[]CommitObservation{{Hash: "deadbeef", Subject: "ship it"}},
		[]repositoryCommit{{hash: full, subject: "ship it"}}, true,
	)
	if len(facts.Subjects) != 1 || len(facts.SHAs) != 0 {
		t.Fatalf("unresolved facts = %#v", facts)
	}
}
