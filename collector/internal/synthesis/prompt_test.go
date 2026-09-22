package synthesis

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestBuildInputsPreservesTurnGroupsAcrossOverflow(t *testing.T) {
	digest := make([]session.DigestEntry, 0, 62)
	for turn := 1; turn <= 30; turn++ {
		marker := fmt.Sprintf("turn-%02d", turn)
		switch turn {
		case 1:
			marker = "early-completed-artifact"
		case 15:
			marker = "middle-correction"
		case 29:
			marker = "unresolved-blocker"
		case 30:
			marker = "recent-next-step"
		}
		digest = append(digest, session.DigestEntry{Turn: turn, Category: session.DigestUser, Description: marker + " " + strings.Repeat("u", 450)})
		if turn == 14 {
			digest = append(digest, session.DigestEntry{Turn: 14, Category: session.DigestQuestion, Description: "grouped-request", Answer: "grouped-answer"})
		}
		digest = append(digest, session.DigestEntry{Turn: turn, Category: session.DigestRecap, Description: "recap-" + marker + " " + strings.Repeat("r", 450)})
	}

	inputs := BuildInputs(&session.Session{ID: "long", Agent: "codex", SessionDetails: session.SessionDetails{Digest: digest}})
	if len(inputs) < 2 {
		t.Fatalf("BuildInputs() returned %d prompt, want overflow chunks", len(inputs))
	}
	joined := strings.Join(inputs, "\n")
	for _, marker := range []string{"early-completed-artifact", "middle-correction", "unresolved-blocker", "recent-next-step"} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("BuildInputs() omitted %q", marker)
		}
	}
	for _, input := range inputs {
		if strings.Contains(input, "grouped-request") &&
			(!strings.Contains(input, "grouped-answer") || !strings.Contains(input, "recap-turn-14")) {
			t.Fatalf("turn group was split: %s", input)
		}
	}
}

func TestBuildInputsIncludesQuestionAnswers(t *testing.T) {
	inputs := BuildInputs(&session.Session{ID: "questions", Agent: "claude", SessionDetails: session.SessionDetails{
		Digest: []session.DigestEntry{{Turn: 1, Category: session.DigestQuestion, Description: "Which option?", Answer: "Option two"}},
	}})
	if len(inputs) != 1 || !strings.Contains(inputs[0], "Option two") {
		t.Fatalf("BuildInputs() omitted question answer: %#v", inputs)
	}
}

func TestBuildInputsKeepsEveryPromptWithinByteBudget(t *testing.T) {
	digest := make([]session.DigestEntry, 50)
	for index := range digest {
		digest[index] = session.DigestEntry{
			Turn: index + 1, Category: session.DigestRecap, Description: fmt.Sprintf("turn-%d %s", index+1, strings.Repeat("界", 500)),
		}
	}
	for _, input := range BuildInputs(&session.Session{ID: "multibyte", Agent: "opencode", SessionDetails: session.SessionDetails{Digest: digest}}) {
		if len(input) > maxPromptBytes {
			t.Fatalf("prompt has %d bytes, want at most %d", len(input), maxPromptBytes)
		}
		if !utf8.ValidString(input) {
			t.Fatal("prompt is not valid UTF-8")
		}
	}
}

func TestBuildInputsHandlesFactsOnlyOverflow(t *testing.T) {
	edits := make([]session.FileEdit, 30)
	for index := range edits {
		edits[index] = session.FileEdit{Path: fmt.Sprintf("artifact-%02d-%s", index, strings.Repeat("x", 450))}
	}
	inputs := BuildInputs(&session.Session{ID: "facts", Agent: "codex", SessionDetails: session.SessionDetails{
		CompactionSeed: strings.Repeat("seed ", 1_000),
		FileEdits:      edits,
	}})
	if len(inputs) != 1 {
		t.Fatalf("BuildInputs() returned %d prompts, want 1", len(inputs))
	}
	if !strings.Contains(inputs[0], "artifact-00") {
		t.Fatalf("BuildInputs() omitted deterministic facts: %s", inputs[0])
	}
}

func TestBuildMergeInputsIncludesDeterministicFactsAndReducesPartials(t *testing.T) {
	partials := make([]session.SessionSynthesis, 8)
	for index := range partials {
		partials[index] = session.SessionSynthesis{Outcome: fmt.Sprintf("history-%d %s", index, strings.Repeat("h", 900))}
	}
	s := &session.Session{ID: "merge", Agent: "codex", SessionDetails: session.SessionDetails{
		Todos:     []session.Todo{{Text: "open-work"}},
		FileEdits: []session.FileEdit{{Path: "artifact.go", Additions: 4, Edits: 1}},
		Commits:   []string{"commit-subject"},
	}}

	inputs := BuildMergeInputs(s, partials)
	if len(inputs) == 0 || len(inputs) >= len(partials) {
		t.Fatalf("BuildMergeInputs() returned %d prompts for %d partials", len(inputs), len(partials))
	}
	joined := strings.Join(inputs, "\n")
	for index := range partials {
		if marker := fmt.Sprintf("history-%d", index); !strings.Contains(joined, marker) {
			t.Fatalf("BuildMergeInputs() omitted %q", marker)
		}
	}
	for _, input := range inputs {
		for _, fact := range []string{"open-work", "artifact.go", "commit-subject"} {
			if !strings.Contains(input, fact) {
				t.Fatalf("merge prompt omitted deterministic fact %q: %s", fact, input)
			}
		}
		if len(input) > maxPromptBytes || !utf8.ValidString(input) {
			t.Fatalf("invalid bounded merge prompt: bytes=%d valid=%v", len(input), utf8.ValidString(input))
		}
	}
}

func TestBuildInputReservesCompactionSeedBeforeDigest(t *testing.T) {
	digest := make([]session.DigestEntry, 40)
	for i := range digest {
		digest[i] = session.DigestEntry{Turn: i + 1, Category: session.DigestRecap, Description: strings.Repeat("digest", 100)}
	}
	input := BuildInput(&session.Session{ID: "session", Agent: "cursor", SessionDetails: session.SessionDetails{
		CompactionSeed: "required pre-compaction context", Digest: digest,
	}})
	if !strings.Contains(input, "COMPACTION SEED\nrequired pre-compaction context") {
		t.Fatalf("compaction seed was truncated from synthesis input: %s", input)
	}
}

func TestBuildInputPreservesDigestAfterMultibyteCompactionSeed(t *testing.T) {
	input := BuildInput(&session.Session{ID: "session", Agent: "cursor", SessionDetails: session.SessionDetails{
		CompactionSeed: strings.Repeat("界", 4_000),
		Digest:         []session.DigestEntry{{Turn: 1, Category: session.DigestRecap, Description: "recent post-compaction fact"}},
	}})
	if !strings.Contains(input, "recent post-compaction fact") {
		t.Fatalf("recent digest was truncated after multibyte compaction seed: %s", input)
	}
}
