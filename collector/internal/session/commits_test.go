package session

import (
	"strconv"
	"strings"
	"testing"
)

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

func TestIsPullRequestCreateExcludesDryRunHelpAndView(t *testing.T) {
	for command, want := range map[string]bool{
		"gh pr create --fill":                      true,
		"rtk gh pr create --title t":               true,
		`gh pr create --title "no --dry-run here"`: true,
		"gh pr create --fill --dry-run":            false,
		"gh pr create --dry-run && echo ok":        false,
		"gh pr create --help":                      false,
		"gh pr view 5":                             false,
	} {
		if got := IsPullRequestCreate(command); got != want {
			t.Errorf("IsPullRequestCreate(%q) = %t, want %t", command, got, want)
		}
	}
}

func TestPullRequestLinksAreCanonical(t *testing.T) {
	urls := map[string]struct{}{
		"https://github.com/acme/web/pull/4":   {},
		"https://github.com/acme/api/pull/10":  {},
		"https://github.com/acme/api/pull/2":   {},
		"https://example.com/acme/api/pull/3":  {},
		"https://github.com/acme/api/pull/0":   {},
		"https://github.com/acme/api/pull/2?x": {},
	}
	got := NewPullRequestLinks(urls)
	want := []string{"https://github.com/acme/api/pull/2", "https://github.com/acme/api/pull/10", "https://github.com/acme/web/pull/4"}
	if len(got) != len(want) {
		t.Fatalf("links = %#v", got)
	}
	for i := range want {
		if got[i].URL != want[i] {
			t.Fatalf("link %d = %q, want %q", i, got[i].URL, want[i])
		}
	}
	if got[1].Repository != "acme/api" || got[1].Number != 10 {
		t.Fatalf("link parts = %#v", got[1])
	}
	if merged := MergePullRequestLinks(got[:2], got[1:]); len(merged) != 3 {
		t.Fatalf("merged = %#v", merged)
	}
	many := map[string]struct{}{}
	for number := 1; number <= MaxPullRequestLinks+5; number++ {
		many["https://github.com/acme/api/pull/"+strconv.Itoa(number)] = struct{}{}
	}
	if got := NewPullRequestLinks(many); len(got) != MaxPullRequestLinks {
		t.Fatalf("capped links = %d", len(got))
	}
	if NewPullRequestLinks(nil) != nil {
		t.Fatal("empty set must give a nil list")
	}
}

func TestDetailRevisionChangesWhenPullRequestLinksChange(t *testing.T) {
	value := Session{Agent: "claude", ID: "s"}
	before, err := LocalDetailRevision(value)
	if err != nil {
		t.Fatal(err)
	}
	value.SetPullRequestLinks([]PullRequestLink{{URL: "https://github.com/acme/api/pull/2", Repository: "acme/api", Number: 2}})
	after, err := LocalDetailRevision(value)
	if err != nil || after == before {
		t.Fatalf("revision must change with the links: %q, %q, %v", before, after, err)
	}
}
