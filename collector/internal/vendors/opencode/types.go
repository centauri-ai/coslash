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
	createdAt    int64
	updatedAt    int64
}

type storedMessage struct {
	Role    string          `json:"role"`
	ModelID string          `json:"modelID"`
	Finish  string          `json:"finish"`
	Summary json.RawMessage `json:"summary"`
	Error   json.RawMessage `json:"error"`
	Tokens  storedTokens    `json:"tokens"`
	parts   []storedPart
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
			Command string `json:"command"`
		} `json:"input"`
	} `json:"state"`
}

type storedDiff struct {
	File      string `json:"file"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
}

type storedModel struct {
	ID string `json:"id"`
}
