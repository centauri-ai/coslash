package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// for codex, every row in JSONL session logs is {timestamp, type, payload}.
type codexRow struct {
	Timestamp string       `json:"timestamp"`
	Type      string       `json:"type"` // session_meta | event_msg | response_item | turn_context | world_state
	Payload   codexPayload `json:"payload"`
}

type codexPayload struct {
	Type           string            `json:"type"`
	Role           string            `json:"role"`
	SessionID      string            `json:"session_id"`
	UserFacingHint string            `json:"user_facing_hint"`
	ForkedFromID   string            `json:"forked_from_id"`
	Cwd            string            `json:"cwd"`
	Originator     string            `json:"originator"`
	Git            *codexGit         `json:"git"`
	Model          string            `json:"model"`
	Message        string            `json:"message"`
	Phase          string            `json:"phase"`
	Info           *codexTokenInfo   `json:"info"`
	Input          string            `json:"input"`
	Arguments      codexArguments    `json:"arguments"`
	Name           string            `json:"name"`
	CallID         string            `json:"call_id"`
	Output         json.RawMessage   `json:"output"`
	Changes        codexPatchChanges `json:"changes"`
	ID             string            `json:"id"`
	Recipient      string            `json:"recipient"`
	Content        json.RawMessage   `json:"content"`
	ParentThreadID string            `json:"parent_thread_id"`
	ThreadSource   string            `json:"thread_source"`
	Source         json.RawMessage   `json:"source"`
	AgentNickname  string            `json:"agent_nickname"`
	AgentThreadID  string            `json:"agent_thread_id"`
	Kind           string            `json:"kind"`
	Item           codexItem         `json:"item"`
}

type codexItem struct {
	Type             string            `json:"type"`
	Phase            string            `json:"phase"`
	Content          []codexText       `json:"content"`
	Changes          codexPatchChanges `json:"changes"`
	Kind             string            `json:"kind"`
	AgentThreadID    string            `json:"agent_thread_id"`
	Command          []string          `json:"command"`
	AggregatedOutput string            `json:"aggregated_output"`
	ExitCode         *int              `json:"exit_code"`
}

func subagentRole(source json.RawMessage) string {
	var parsed struct {
		Subagent struct {
			Other string `json:"other"`
		} `json:"subagent"`
	}
	if json.Unmarshal(source, &parsed) != nil {
		return ""
	}
	return parsed.Subagent.Other
}

type codexText struct {
	Text string `json:"text"`
}

type codexGit struct {
	Branch string `json:"branch"`
}
type codexArguments string

func (a *codexArguments) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = codexArguments(s)
		return nil
	}
	*a = codexArguments(b)
	return nil
}

type planStep struct {
	step   string
	status string
}

type codexPatchChange struct {
	Type        string `json:"type"` // "update" | "add"
	UnifiedDiff string `json:"unified_diff"`
	Content     string `json:"content"`
}

type codexPatchChangeEntry struct {
	Path   string
	Change codexPatchChange
}

type codexPatchChanges []codexPatchChangeEntry

func (changes *codexPatchChanges) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*changes = nil
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("patch changes must be a JSON object")
	}
	var entries []codexPatchChangeEntry
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		path, ok := token.(string)
		if !ok {
			return fmt.Errorf("patch change key must be a string")
		}
		var change codexPatchChange
		if err := decoder.Decode(&change); err != nil {
			return err
		}
		entries = append(entries, codexPatchChangeEntry{Path: path, Change: change})
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*changes = entries
	return nil
}

type codexTokenInfo struct {
	TotalTokenUsage    codexTokenUsage `json:"total_token_usage"`
	LastTokenUsage     codexTokenUsage `json:"last_token_usage"`
	ModelContextWindow int             `json:"model_context_window"`
}

type codexTokenUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	CacheWriteInputTokens int `json:"cache_write_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
}

// codexTokenSample pairs Codex's cumulative token counters with the model
// active when that counter was recorded. A rollout can switch models, so the
// final total alone cannot be priced correctly.
type codexTokenSample struct {
	model string
	usage codexTokenUsage
}

// codexMeta is the lineage- and parentage-bearing part of a session_meta row.
// A fork inlines one per ancestor, so they are collected and the rollout's own
// is resolved once the whole file is read (see ownMeta). For a subagent,
// sessionID is the PARENT's id — only payload.id (= the filename uuid) is its
// own.
type codexMeta struct {
	payloadID        string
	sessionID        string
	forkedFromID     string
	parentThreadID   string
	subagentRole     string
	agentNickname    string
	workingDirectory string
	branch           string
	entrypoint       string
}
