package claude

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

var skillCat = regexp.MustCompile(`^\s*cat\s+(?:--\s+)?(?:'([^'\r\n]+)'|"([^"$` + "`" + `\r\n]+)"|([^\s;'"&|<>$` + "`" + `]+))\s*$`)
var readLineNumber = regexp.MustCompile(`(?m)^[ \t]*\d+→([^\n]*(?:\n|$))`)

type skillCall struct {
	name, body           string
	native, read, failed bool
	entry                *session.Skill
}

type claudeSkills struct {
	calls   map[string]*skillCall
	entries []*session.Skill
	slash   string
	child   bool
}

func (skills *claudeSkills) note(row claudeSessionRecord) {
	if row.Message == nil || row.IsCompactSummary {
		return
	}
	if row.Type == "user" {
		if command := row.commandInvocation(); command != "" {
			skills.slash = strings.TrimPrefix(strings.Fields(command)[0], "/")
		} else if row.IsMeta {
			skills.noteInstructions(row)
			skills.slash = ""
		} else {
			skills.slash = ""
		}
	}
	blocks, err := row.Message.contentBlocks()
	if err != nil {
		return
	}
	for _, block := range blocks {
		if row.Type == "assistant" && block.Type == "tool_use" && block.ID != "" {
			call := &skillCall{}
			path := ""
			switch block.Name {
			case "Skill":
				call.name, call.native = block.Input.Skill, true
			case "Read":
				path, call.read = block.Input.FilePath, true
			case "Bash":
				if match := skillCat.FindStringSubmatch(block.Input.Command); match != nil {
					path = strings.Join(match[1:], "")
				}
			default:
				continue
			}
			if !call.native {
				skill, ok := session.SkillFromPath(path, "")
				if !ok {
					continue
				}
				call.name = skill.Name
			}
			if skills.calls == nil {
				skills.calls = map[string]*skillCall{}
			}
			skills.calls[block.ID] = call
		}
		if row.Type != "user" || block.Type != "tool_result" {
			continue
		}
		call := skills.calls[block.ToolUseID]
		if call == nil || call.entry != nil || call.failed {
			continue
		}
		var result struct {
			Success     *bool  `json:"success"`
			CommandName string `json:"commandName"`
			File        *struct {
				Content string `json:"content"`
			} `json:"file"`
		}
		_ = json.Unmarshal(row.ToolUseResult, &result)
		if block.IsError || (result.Success != nil && !*result.Success) {
			call.failed = true
			continue
		}
		if call.native {
			if call.name == "" {
				call.name = result.CommandName
			}
		} else {
			call.body = claudeToolOutput(block.Content)
			if call.read {
				if result.File != nil {
					call.body = result.File.Content
				} else {
					if lines := readLineNumber.FindAllStringSubmatch(call.body, -1); len(lines) > 0 {
						var body strings.Builder
						for _, line := range lines {
							body.WriteString(line[1])
						}
						call.body = body.String()
					}
				}
			}
			if strings.HasPrefix(call.body, "<persisted-output>") {
				call.body = ""
			}
		}
		call.entry = &session.Skill{Name: call.name, Content: call.body}
		skills.entries = append(skills.entries, call.entry)
	}
}

func (skills *claudeSkills) noteInstructions(row claudeSessionRecord) {
	header, body, _ := strings.Cut(row.Message.textContent(), "\n")
	directory, ok := strings.CutPrefix(strings.TrimSuffix(header, "\r"), "Base directory for this skill: ")
	if !ok || directory == "" {
		return
	}
	body = strings.TrimPrefix(strings.TrimPrefix(body, "\r\n"), "\n")
	if call := skills.calls[row.SourceToolUseID]; call != nil && call.native {
		if !call.failed {
			call.body = body
			if call.entry != nil {
				call.entry.Content = body
			}
		}
		return
	}
	if skills.slash == "" && !skills.child {
		return
	}
	skill, ok := session.SkillFromPath(strings.TrimRight(directory, "/\\")+"/SKILL.md", body)
	if !ok {
		return
	}
	if skills.slash != "" {
		skill.Name = skills.slash
	}
	skills.entries = append(skills.entries, &skill)
}

func (skills *claudeSkills) log() session.SkillLog {
	var log session.SkillLog
	for _, entry := range skills.entries {
		log.Note(entry.Name, entry.Content)
	}
	return log
}
