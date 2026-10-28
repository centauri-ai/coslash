package fullsessionrecord

import (
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"reflect"
	"testing"
)

func TestSkillContentSurvivesPortableRoundTrip(t *testing.T) {
	original := session.Session{Agent: "codex", ID: "root", StartedAt: 1, LastActivityTime: 2, SessionDetails: session.SessionDetails{Skills: []session.Skill{{Name: "review", Content: "# First\n  body\n"}, {Name: "review", Content: "# Second\n"}}}}
	record, err := FromSession("local", original)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ToSession(record)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Skills, original.Skills) {
		t.Fatalf("skills lost: %#v", restored.Skills)
	}
	original.Skills[0].Content = "changed"
	if record.Session.Skills[0].Content != "# First\n  body\n" {
		t.Fatal("record aliases original")
	}
}
func TestFamilySkillRollupRetainsVariantsAndLeavesInputUnchanged(t *testing.T) {
	parsed := []*vendors.ParsedSession{}
	for i, id := range []string{"root", "child", "grandchild"} {
		item := &vendors.ParsedSession{Session: &session.Session{Agent: "codex", ID: id, StartedAt: 1, LastActivityTime: 2, SessionDetails: session.SessionDetails{Turns: 1, Skills: []session.Skill{{Name: "review", Content: []string{"first", "first", "second"}[i]}}}}, Spawns: map[string]vendors.SpawnState{}}
		if i > 0 {
			item.ParentID = []string{"root", "child"}[i-1]
		}
		parsed = append(parsed, item)
	}
	records, err := FromParsedFamily("local", "codex", vendors.LocalReadSource, parsed, vendors.EmptySessionMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || len(records[0].Session.Skills) != 2 || len(records[1].Session.Skills) != 2 || records[2].Session.Skills[0].Content != "second" {
		t.Fatalf("family skills not composed: %#v", records)
	}
	if len(parsed[0].Session.Skills) != 1 {
		t.Fatal("composition mutated parsed input")
	}
}
