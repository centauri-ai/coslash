package pi

import "encoding/json"

// These facts retain persisted identity and accounting without attributing forks.
type transcript struct {
	Path        string
	Header      header
	Entries     []entry
	ByID        map[string]int
	Diagnostics []string
	Incomplete  bool
}

type header struct {
	Type          string          `json:"type"`
	Version       int             `json:"version"`
	ID            string          `json:"id"`
	Timestamp     string          `json:"timestamp"`
	CWD           string          `json:"cwd"`
	ParentSession string          `json:"parentSession"`
	Raw           json.RawMessage `json:"-"`
}

type entry struct {
	Type                   string          `json:"type"`
	ID                     string          `json:"id"`
	ParentID               *string         `json:"parentId"`
	Timestamp              string          `json:"timestamp"`
	AppendIndex            int             `json:"-"`
	Raw                    json.RawMessage `json:"-"`
	Usage                  *usageFact      `json:"-"`
	NestedDetailIncomplete bool            `json:"-"`
}

type usageFact struct {
	Source       string          `json:"-"`
	Provider     string          `json:"-"`
	Model        string          `json:"-"`
	Raw          json.RawMessage `json:"-"`
	Input        *int            `json:"input"`
	Output       *int            `json:"output"`
	CacheRead    *int            `json:"cacheRead"`
	CacheWrite   *int            `json:"cacheWrite"`
	CacheWrite1h *int            `json:"cacheWrite1h"`
	TotalTokens  *int            `json:"totalTokens"`
	Cost         *struct {
		Total *float64 `json:"total"`
	} `json:"cost"`
	TokensMissing bool `json:"-"`
	CostMissing   bool `json:"-"`
	ZeroTokens    bool `json:"-"`
	ZeroCost      bool `json:"-"`
}
