package claude

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

var (
	commandNameRe = regexp.MustCompile(`<command-name>\s*(/[^<\s]+)`)
	commandArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

type claudeSessionRecord struct {
	SessionID            string          `json:"sessionId"`
	SessionKind          string          `json:"sessionKind"`
	RowUUID              string          `json:"uuid"`
	Entrypoint           *string         `json:"entrypoint"`
	WorkingDirectory     string          `json:"cwd"`
	Branch               *string         `json:"gitBranch"`
	Timestamp            string          `json:"timestamp"`
	CustomTitle          string          `json:"customTitle"`
	CustomSessionName    string          `json:"agentName"`
	GeneratedSessionName string          `json:"aiTitle"`
	Type                 string          `json:"type"`
	Subtype              string          `json:"subtype"`
	PRURL                string          `json:"prUrl"`
	Content              string          `json:"content"`
	IsCompactSummary     bool            `json:"isCompactSummary"`
	IsMeta               bool            `json:"isMeta"`
	TurnDurationMs       *int            `json:"durationMs"`
	Message              *claudeMessage  `json:"message"`
	ToolUseResult        json.RawMessage `json:"toolUseResult"`
}

type claudeMessage struct {
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	Usage      *claudeUsage    `json:"usage"`
	ID         string          `json:"id"`
	StopReason string          `json:"stop_reason"`
}

type claudeUsage struct {
	InputTokens           int `json:"input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	CacheReadInputTokens  int `json:"cache_read_input_tokens"`
	CacheCreationTotal    int `json:"cache_creation_input_tokens"`
	CacheWriteInputTokens struct {
		Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
		Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

func (usage *claudeUsage) untieredCacheCreation() int {
	tiered := usage.CacheWriteInputTokens.Ephemeral1h + usage.CacheWriteInputTokens.Ephemeral5m
	return max(0, usage.CacheCreationTotal-tiered)
}

type contentBlock struct {
	Type      string          `json:"type"` // "text" | "tool_use" | "tool_result"
	Text      string          `json:"text"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"` // "Bash", "Edit", "Task", ...
	IsError   bool            `json:"is_error"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	Input     toolInput       `json:"input"`
}

// agent-<id>.meta.json, written beside each subagent transcript
type subagentMeta struct {
	AgentType     string `json:"agentType"`
	Description   string `json:"description"`
	ToolUseID     string `json:"toolUseId"`
	StoppedByUser bool   `json:"stoppedByUser"`
}

type toolInput struct {
	FilePath    string `json:"file_path"`
	Command     string `json:"command"`
	Description string `json:"description"`
	TaskID      string `json:"taskId"`
	Status      string `json:"status"`
}

type claudeAnswer []string

func (answer *claudeAnswer) UnmarshalJSON(data []byte) error {
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err == nil {
		*answer = multiple
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	*answer = []string{single}
	return nil
}

func (answer claudeAnswer) String() string {
	return strings.Join(answer, ", ")
}

type claudeToolUseResult struct {
	FilePath        string          `json:"filePath"`
	Type            string          `json:"type"`
	Content         json.RawMessage `json:"content"`
	StructuredPatch []struct {
		OldStart int      `json:"oldStart"`
		OldLines int      `json:"oldLines"`
		NewStart int      `json:"newStart"`
		NewLines int      `json:"newLines"`
		Lines    []string `json:"lines"`
	} `json:"structuredPatch"`
	Task *struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
	} `json:"task"`
	Questions []struct {
		Question string `json:"question"`
	} `json:"questions"`
	Answers map[string]claudeAnswer `json:"answers"`
	RunID   string                  `json:"runId"` // Workflow launches, keyed to the run's agents
}

func (message *claudeMessage) contentBlocks() ([]contentBlock, error) {
	trimmed := bytes.TrimSpace(message.Content)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(message.Content, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

func (message *claudeMessage) textContent() string {
	trimmed := bytes.TrimSpace(message.Content)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(message.Content, &text); err != nil {
			return ""
		}
		return text
	}
	blocks, err := message.contentBlocks()
	if err != nil {
		return ""
	}
	for _, block := range blocks {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

func (message *claudeMessage) userToolFeedback() string {
	blocks, err := message.contentBlocks()
	if err != nil {
		return ""
	}
	const marker = "To tell you how to proceed, the user said:\n"
	for _, block := range blocks {
		if block.Type != "tool_result" || !block.IsError {
			continue
		}
		var content string
		if err := json.Unmarshal(block.Content, &content); err != nil {
			continue
		}
		if _, feedback, ok := strings.Cut(content, marker); ok {
			return strings.TrimSpace(feedback)
		}
	}
	return ""
}

func (row *claudeSessionRecord) commandInvocation() string {
	if row.IsMeta || row.Message == nil {
		return ""
	}
	text := row.Message.textContent()
	name := commandNameRe.FindStringSubmatch(text)
	if name == nil {
		return ""
	}
	command := name[1]
	if args := commandArgsRe.FindStringSubmatch(text); args != nil {
		if trimmed := strings.TrimSpace(args[1]); trimmed != "" {
			command += " " + trimmed
		}
	}
	return command
}

func (row *claudeSessionRecord) promptText() string {
	if row.IsMeta || row.IsCompactSummary || row.Message == nil {
		return ""
	}
	text := row.Message.textContent()
	if text == "" {
		text = row.Message.userToolFeedback()
	}
	if text == "" || session.IsHarnessWrapped(text) {
		return ""
	}
	return text
}

func (row *claudeSessionRecord) toolResult() (*claudeToolUseResult, error) {
	trimmed := bytes.TrimSpace(row.ToolUseResult)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, nil
	}
	var result claudeToolUseResult
	if err := json.Unmarshal(row.ToolUseResult, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
