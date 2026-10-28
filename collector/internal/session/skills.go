package session

import (
	"crypto/sha256"
	"fmt"
	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"path"
	"strings"
	"unicode/utf8"
)

type Skill = fullsessionv1.Skill

type SkillLog struct {
	entries []Skill
	seen    map[Skill]bool
}

func (log *SkillLog) Note(name, content string) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > fullsessionv1.MaxStringBytes {
		return
	}
	content = withoutNUL(strings.ToValidUTF8(content, "\uFFFD"))
	if len(content) > fullsessionv1.MaxStringBytes {
		marker := fmt.Sprintf("\n[Skill content truncated; sha256=%x]", sha256.Sum256([]byte(content)))
		end := fullsessionv1.MaxStringBytes - len(marker)
		for !utf8.RuneStart(content[end]) {
			end--
		}
		content = strings.Clone(content[:end]) + marker
	}
	skill := Skill{Name: name, Content: content}
	if log.seen[skill] {
		return
	}
	if log.seen == nil {
		log.seen = make(map[Skill]bool)
	}
	log.seen[skill] = true
	log.entries = append(log.entries, skill)
}

func (log *SkillLog) Entries() []Skill { return log.entries }

func SkillFromPath(file, content string) (Skill, bool) {
	file = strings.ReplaceAll(file, "\\", "/")
	if path.Base(file) != "SKILL.md" {
		return Skill{}, false
	}
	name := path.Base(path.Dir(file))
	if name == "." || name == "/" || name == "" {
		return Skill{}, false
	}
	return Skill{Name: name, Content: content}, true
}

func MergeSkills(groups ...[]Skill) []Skill {
	var log SkillLog
	for _, group := range groups {
		for _, skill := range group {
			log.Note(skill.Name, skill.Content)
		}
	}
	return log.Entries()
}
