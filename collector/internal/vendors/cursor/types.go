package cursor

import "encoding/json"

type transcriptRecord struct {
	Role    string             `json:"role"`
	Type    string             `json:"type"`
	Message *transcriptMessage `json:"message"`
	Status  string             `json:"status"`
	Error   string             `json:"error"`
}

type transcriptMessage struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type toolInput struct {
	Command          string         `json:"command"`
	Description      string         `json:"description"`
	Prompt           string         `json:"prompt"`
	SubagentType     string         `json:"subagent_type"`
	WorkingDirectory string         `json:"working_directory"`
	Path             string         `json:"path"`
	Contents         string         `json:"contents"`
	OldString        string         `json:"old_string"`
	NewString        string         `json:"new_string"`
	Patch            string         `json:"patch"`
	Todos            []todoToolItem `json:"todos"`
}

type todoToolItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}
