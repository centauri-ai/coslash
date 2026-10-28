package fullsessionv1

import (
	"errors"
	"strings"
	"testing"
)

func TestSkillRecordValidationAndFreezeOwnership(t *testing.T) {
	base := Record{SourceID: "local", Agent: "codex", SessionID: "root", Session: Session{StartedAtMs: 1, LastActivityAtMs: 1, Skills: []Skill{{Name: "review", Content: "body"}}}}
	frozen, err := Freeze(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Session.Skills[0].Content = "changed"
	if frozen.Session.Skills[0].Content != "body" {
		t.Fatal("frozen record aliases skills")
	}
	body, err := Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(body)
	if err != nil || len(decoded.Session.Skills) != 1 {
		t.Fatalf("round trip: %v", err)
	}
	for _, skill := range []Skill{{Name: "", Content: "body"}, {Name: "review", Content: "bad\x00body"}, {Name: "review", Content: strings.Repeat("x", MaxStringBytes+1)}} {
		base.Session.Skills = []Skill{skill}
		if _, err := Freeze(base); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid skill accepted: %v", err)
		}
	}
	base.Session.Skills = make([]Skill, MaxItems+1)
	if _, err := Freeze(base); !errors.Is(err, ErrInvalid) {
		t.Fatalf("item limit bypassed: %v", err)
	}
}
