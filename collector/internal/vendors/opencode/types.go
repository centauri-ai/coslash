package opencode

import (
	"database/sql"
	"encoding/json"
)

type SourceHealth struct {
	Entries  int
	Sessions int
	Missing  bool
}

type storedSession struct {
	id           string
	directory    string
	title        string
	summaryFiles sql.NullInt64
	summaryDiffs sql.NullString
	model        sql.NullString
	cost         float64
	updatedAt    int64
}

type storedMessage struct {
	Role       string          `json:"role"`
	ProviderID string          `json:"providerID"`
	ModelID    string          `json:"modelID"`
	Finish     string          `json:"finish"`
	Summary    json.RawMessage `json:"summary"`
	Error      json.RawMessage `json:"error"`
	Cost       float64         `json:"cost"`
	Tokens     storedTokens    `json:"tokens"`
	Time       struct {
		Created   int64  `json:"created"`
		Completed *int64 `json:"completed"`
	} `json:"time"`
	parts []storedPart
}

type storedTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Cache     struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

type storedPart struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic"`
	Tool      string `json:"tool"`
	State     struct {
		Status string `json:"status"`
		Title  string `json:"title"`
		Input  struct {
			Command   string           `json:"command"`
			FilePath  string           `json:"filePath"`
			Content   string           `json:"content"`
			Questions []storedQuestion `json:"questions"`
			Todos     []struct {
				Content string `json:"content"`
				Status  string `json:"status"`
			} `json:"todos"`
		} `json:"input"`
		Metadata struct {
			Answers  [][]string       `json:"answers"`
			Exists   *bool            `json:"exists"`
			Exit     *int             `json:"exit"`
			FileDiff storedToolFile   `json:"filediff"`
			FilePath string           `json:"filepath"`
			Files    []storedToolFile `json:"files"`
		} `json:"metadata"`
		Time struct {
			End *int64 `json:"end"`
		} `json:"time"`
	} `json:"state"`
}

type storedToolFile struct {
	File         string `json:"file"`
	FilePath     string `json:"filePath"`
	RelativePath string `json:"relativePath"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Type         string `json:"type"`
}

type storedQuestion struct {
	Question string `json:"question"`
}

type storedDiff struct {
	File      string `json:"file"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
}

type storedModel struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
}
