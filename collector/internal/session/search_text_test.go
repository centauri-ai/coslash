package session

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSearchTextBoundsAndRevision(t *testing.T) {
	var log DigestLog
	log.PushQuestion(1, strings.Repeat("é", 5000), "answer", 0)
	for i := 0; i < 20; i++ {
		log.Push(i+2, DigestUser, strings.Repeat("x", 8192), 0)
	}
	total := 0
	for _, e := range log.Entries() {
		total += len(e.SearchText)
		if len(e.SearchText) > MaxSearchEntryBytes || !utf8.ValidString(e.SearchText) {
			t.Fatal("invalid entry bound")
		}
	}
	if total > MaxSearchSessionBytes || !SearchTextPartial(log.Entries()) {
		t.Fatal("invalid session bound")
	}
	s := Session{Agent: "claude", ID: "test", SessionDetails: SessionDetails{Digest: log.Entries()}}
	before, err := LocalDetailRevision(s)
	if err != nil {
		t.Fatal(err)
	}
	copy := Clone(&s)
	for i := range copy.Digest {
		copy.Digest[i].SearchText = "private needle"
		copy.Digest[i].SearchTextTruncated = false
	}
	copy.Synthesis = &SessionSynthesis{Records: []DecisionRecord{{Text: "local", Turns: []int{1}, Evidence: []DecisionEvidence{{Turn: 1, Text: "local"}}}}}
	after, err := LocalDetailRevision(*copy)
	if err != nil || before != after {
		t.Fatal("local enrichment changed revision")
	}
	data, _ := json.Marshal(copy.Digest)
	if strings.Contains(string(data), "private needle") {
		t.Fatal("search text leaked into JSON")
	}
	cloned := Clone(copy)
	cloned.Synthesis.Records[0].Turns[0] = 99
	cloned.Synthesis.Records[0].Evidence[0].Text = "changed"
	if copy.Synthesis.Records[0].Turns[0] != 1 || copy.Synthesis.Records[0].Evidence[0].Text != "local" {
		t.Fatal("records alias")
	}
}

func TestSearchQuestionPreservesFullAnswer(t *testing.T) {
	var log DigestLog
	answer := strings.Repeat("answer ", 100)
	log.PushQuestion(1, "question", answer, 0)
	entry := log.Entries()[0]
	if entry.SearchText != "question\n"+answer || len(entry.Answer) >= len(answer) || SearchTextPartial(log.Entries()) {
		t.Fatal("question capture or display changed")
	}
}
