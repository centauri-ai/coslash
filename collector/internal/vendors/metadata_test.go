package vendors

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestApplySessionEnrichmentCopiesTokenMap(t *testing.T) {
	metadataTokens := map[string]session.ModelTokens{"gpt-5": {InputTokens: 10}}
	parsed := &ParsedSession{Session: &session.Session{}}
	ApplySessionEnrichment(parsed, &SessionEnrichment{Usage: SessionUsage{Tokens: metadataTokens}})

	used := parsed.Session.Tokens["gpt-5"]
	used.Cost = 1
	parsed.Session.Tokens["gpt-5"] = used
	if got := metadataTokens["gpt-5"].Cost; got != 0 {
		t.Fatalf("metadata token cost mutated to %v", got)
	}
}

func TestApplySessionEnrichmentMergesPullRequestLinksAsUnion(t *testing.T) {
	link := func(number int) session.PullRequestLink {
		return session.PullRequestLink{URL: "https://github.com/acme/api/pull/" + strconv.Itoa(number), Repository: "acme/api", Number: number}
	}
	parsed := &ParsedSession{Session: &session.Session{}}
	parsed.Session.SetPullRequestLinks([]session.PullRequestLink{link(1), link(2)})
	ApplySessionEnrichment(parsed, &SessionEnrichment{PullRequests: 2, PullRequestLinks: []session.PullRequestLink{link(2), link(3)}})
	if got := parsed.Session.PullRequestLinks; !reflect.DeepEqual(got, []session.PullRequestLink{link(1), link(2), link(3)}) || parsed.Session.PullRequests != 3 {
		t.Fatalf("links = %#v, count = %d", got, parsed.Session.PullRequests)
	}
	counted := &ParsedSession{Session: &session.Session{}}
	counted.Session.PullRequests = 2
	ApplySessionEnrichment(counted, &SessionEnrichment{PullRequests: 1})
	if counted.Session.PullRequests != 2 {
		t.Fatalf("a count without links must keep the larger value, got %d", counted.Session.PullRequests)
	}
}
