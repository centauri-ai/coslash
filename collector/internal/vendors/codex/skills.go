package codex

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

var skillOutputPattern = regexp.MustCompile(`(?s)^(?:Chunk ID: [^\n]*\n)?Wall time: [^\n]*\nProcess exited with code 0\nFinal output:\n(.*)$`)

var skillReadPattern = regexp.MustCompile(`^(?:rtk (?:proxy )?)?(?:cat(?: --)?|sed -n (?:'[0-9]+(?:,[0-9]+)?p'|"[0-9]+(?:,[0-9]+)?p"|[0-9]+(?:,[0-9]+)?p))\s+("[^"$\x60]*"|'[^']*'|[^\s'"$\x60;|&<>()]+)\s*$`)

type codexSkills struct {
	log     session.SkillLog
	pending map[string]codexPayload
}

func (skills *codexSkills) note(payload codexPayload) {
	switch payload.Type {
	case "message":
		if payload.Role != "user" {
			return
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(payload.Content, &blocks) != nil {
			return
		}
		for _, block := range blocks {
			if block.Type != "input_text" {
				continue
			}
			text, ok := strings.CutPrefix(block.Text, "<skill>\n<name>")
			if !ok {
				continue
			}
			name, text, ok := strings.Cut(text, "</name>\n<path>")
			if !ok || name == "" {
				continue
			}
			path, text, ok := strings.Cut(text, "</path>\n")
			if !ok {
				continue
			}
			body, ok := strings.CutSuffix(text, "\n</skill>")
			if _, valid := session.SkillFromPath(path, body); ok && valid {
				skills.log.Note(name, body)
			}
		}
	case "function_call", "custom_tool_call":
		if payload.CallID == "" || (payload.Name != "exec_command" && payload.Name != "exec" && payload.Name != "skills.read") {
			return
		}
		if skills.pending == nil {
			skills.pending = make(map[string]codexPayload)
		}
		skills.pending[payload.CallID] = payload
	case "function_call_output", "custom_tool_call_output":
		call, ok := skills.pending[payload.CallID]
		if !ok {
			return
		}
		delete(skills.pending, payload.CallID)
		if call.Name == "skills.read" {
			var args struct {
				Resource string `json:"resource"`
			}
			var result struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			}
			raw := payload.Output
			var encoded string
			if json.Unmarshal(raw, &encoded) == nil {
				raw = json.RawMessage(encoded)
			}
			if json.Unmarshal([]byte(payloadText(call)), &args) != nil || json.Unmarshal(raw, &result) != nil || result.IsError {
				return
			}
			var parts []string
			for _, block := range result.Content {
				if block.Type == "text" {
					parts = append(parts, block.Text)
				}
			}
			if len(parts) > 0 {
				skills.notePath(args.Resource, strings.Join(parts, "\n"))
			}
			return
		}
		if call.Name == "exec_command" {
			if command, ok := commandFrom(call); ok {
				skills.noteRead(command, payload.Output)
			}
			return
		}
		// Pair only captured exec results, never the script's completion status.
		commands := commandsFrom(call)
		results := decodeCodexCommandResults(payload.Output)
		if len(commands) != len(results) {
			return
		}
		for i, command := range commands {
			if results[i].succeeded {
				skills.noteSuccessfulRead(command, results[i].output)
			}
		}
	}
}

func (skills *codexSkills) noteRead(command string, raw json.RawMessage) {
	result := decodeCodexCommandResult(raw)
	// Native exec_command wrappers also put the exit code inside metadata.
	if !result.succeeded {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			raw = json.RawMessage(text)
		}
		var wrapped struct {
			Output   *string `json:"output"`
			ExitCode *int    `json:"exit_code"`
			Metadata struct {
				ExitCode *int `json:"exit_code"`
			} `json:"metadata"`
		}
		if json.Unmarshal(raw, &wrapped) == nil && wrapped.Output != nil && wrapped.ExitCode == nil && wrapped.Metadata.ExitCode != nil && *wrapped.Metadata.ExitCode == 0 {
			result = codexCommandResult{output: *wrapped.Output, succeeded: true}
		}
	}
	if !result.succeeded {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			text = string(raw)
		}
		if match := skillOutputPattern.FindStringSubmatch(text); len(match) > 0 {
			result = codexCommandResult{output: match[1], succeeded: true}
		}
	}
	if result.succeeded {
		skills.noteSuccessfulRead(command, result.output)
	}
}

func (skills *codexSkills) notePath(path, content string) {
	if skill, ok := session.SkillFromPath(path, content); ok {
		skills.log.Note(skill.Name, skill.Content)
	}
}

func (skills *codexSkills) noteSuccessfulRead(command, output string) {
	match := skillReadPattern.FindStringSubmatch(strings.TrimSpace(command))
	if len(match) > 0 {
		skills.notePath(strings.Trim(match[1], "\"'"), output)
	}
}
