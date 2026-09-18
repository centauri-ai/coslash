package synthesis

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

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
