package session

import (
	"strings"
	"testing"
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

func TestLongFormDigestRespectsFullSessionStringLimit(t *testing.T) {
	prefix := strings.Repeat("x", fullsessionv1.MaxStringBytes-1)
	for _, category := range []string{DigestPlan, DigestRecap} {
		t.Run(category, func(t *testing.T) {
			var log DigestLog
			log.Push(1, category, prefix+"x", 0)
			log.Push(2, category, prefix+"é and more", 0)
			entries := log.Entries()
			if entries[0].Description != prefix+"x" {
				t.Fatal("digest at the byte limit was changed")
			}
			if entries[1].Description != prefix || !utf8.ValidString(entries[1].Description) {
				t.Fatalf("oversized digest was not bounded at a UTF-8 boundary: %d bytes", len(entries[1].Description))
			}
		})
	}
}
