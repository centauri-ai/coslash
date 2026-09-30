package synthesis

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

// Revision reuses the untruncated parent revision assigned during composition.
// Pi's selected source context changes even when the transcript does not append.
func Revision(s *session.Session) int64 {
	if s.SynthesisRevision > 0 {
		return s.SynthesisRevision
	}
	if s.Agent != "pi" {
		return s.LastActivityTime
	}
	digest := session.SelectedDigest(s.Digest)
	for i := range digest {
		digest[i].Active = nil
		digest[i].Inherited = nil
		digest[i].ContextSelected = nil
		digest[i].ContextDescription = nil
	}
	payload, _ := json.Marshal(struct {
		Digest               []session.DigestEntry
		Summary, FirstPrompt *string
		Seed                 string
		Todos                []session.Todo
		Modified             int64
	}{digest, s.Summary, s.FirstPrompt, s.CompactionSeed, s.Todos, s.LastActivityTime})
	sum := sha256.Sum256(payload)
	// JSON numbers must round-trip exactly through the browser.
	return int64(binary.BigEndian.Uint64(sum[:8])%((1<<53)-1)) + 1
}
