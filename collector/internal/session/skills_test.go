package session

import (
	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSkillLogPreservesContentVariants(t *testing.T) {
	var log SkillLog
	if log.Entries() != nil {
		t.Fatal("empty log must remain absent")
	}
	log.Note("review", "# Review\n  first body\n")
	log.Note("review", "# Review\n  first body\n")
	log.Note("review", "# Review\n  second body\n")
	log.Note("", "not a skill")
	got := log.Entries()
	if len(got) != 2 || got[0].Content != "# Review\n  first body\n" || got[1].Content != "# Review\n  second body\n" {
		t.Fatalf("skills = %#v", got)
	}
}
func TestSkillFromPathRecognizesOnlyManifestReads(t *testing.T) {
	for _, path := range []string{"/skills/review/SKILL.md", `C:\skills\review\SKILL.md`} {
		s, ok := SkillFromPath(path, "body")
		if !ok || s.Name != "review" || s.Content != "body" {
			t.Fatalf("%q = %#v, %v", path, s, ok)
		}
	}
	for _, path := range []string{"/skills/review/README.md", "SKILL.md", "/skills/review/SKILL.md.bak"} {
		if _, ok := SkillFromPath(path, "body"); ok {
			t.Fatalf("accepted %q", path)
		}
	}
}
func TestSkillLogBoundsUntrustedContentWithoutBreakingUTF8(t *testing.T) {
	var log SkillLog
	log.Note("review", strings.Repeat("界", fullsessionv1.MaxStringBytes)+"\x00")
	got := log.Entries()[0].Content
	if len(got) > fullsessionv1.MaxStringBytes || !utf8.ValidString(got) || strings.ContainsRune(got, 0) || !strings.HasSuffix(got, "[Skill content truncated]") {
		t.Fatalf("invalid bounded content: %d bytes", len(got))
	}
}
