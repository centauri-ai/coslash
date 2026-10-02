package session

import (
	"context"
	"strings"
	"testing"
)

func TestNewCommitFactsReconcilerContextWithoutRepository(t *testing.T) {
	observations := []CommitObservation{
		{Hash: "deadbeef", Subject: "initial"},
		{Hash: "cafebabe", Subject: "amended", Amend: true},
	}
	reconcile := NewCommitFactsReconcilerContext(context.Background())
	facts := reconcile(observations, "", nil)
	if len(facts.Subjects) != 1 || facts.Subjects[0] != "amended" || facts.SHAs == nil || len(facts.SHAs) != 0 {
		t.Fatalf("fallback facts = %#v, want amended subject without SHAs", facts)
	}
	facts = reconcile(nil, "", nil)
	if facts.Subjects == nil || len(facts.Subjects) != 0 || facts.SHAs == nil || len(facts.SHAs) != 0 {
		t.Fatalf("empty facts = %#v, want non-nil empty slices", facts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	facts = NewCommitFactsReconcilerContext(ctx)(observations, "", nil)
	if facts.Subjects != nil || facts.SHAs == nil || len(facts.SHAs) != 0 {
		t.Fatalf("canceled facts = %#v, want no subjects or SHAs", facts)
	}
}

func TestParseCommitObservationsIgnoresHexLikeBranchNames(t *testing.T) {
	got := ParseCommitObservations("git commit -m 'ship it'", "[cursor/4721ccdf 6a875286] ship it\n", true)
	if len(got) != 1 || got[0].Hash != "6a875286" {
		t.Fatalf("commit observations = %v, want commit hash", got)
	}
}

func TestParseCommitObservationsSupportsStandaloneFullHash(t *testing.T) {
	hash := strings.Repeat("a", 40)
	got := ParseCommitObservations("git commit --quiet -m 'ship it' && git rev-parse HEAD", hash+"\n", true)
	if len(got) != 1 || got[0].Hash != hash {
		t.Fatalf("commit observations = %v, want standalone commit hash", got)
	}
}

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
