package session

type ModelTokens struct {
	InputTokens                int     `json:"input_tokens"`
	OutputTokens               int     `json:"output_tokens"`
	CacheCreationInputTokens   int     `json:"cache_creation_input_tokens"`
	CacheCreation1hInputTokens int     `json:"cache_creation_1h_input_tokens"`
	CacheReadInputTokens       int     `json:"cache_read_input_tokens"`
	Cost                       float64 `json:"cost,omitempty"`
}

const TruncateTextLimit = 280

const (
	SubagentRunning  = "running"
	SubagentReturned = "returned"
	SubagentAborted  = "aborted"
)

type SubagentCommand struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type Subagent struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Model         *string                `json:"model"`
	Status        string                 `json:"status"`
	Task          string                 `json:"task"`
	Result        string                 `json:"result"`
	DurationMs    *int                   `json:"durationMs"`
	SpawnedAtTurn *int                   `json:"spawnedAtTurn"`
	ToolUses      int                    `json:"toolUses"`
	Commands      []SubagentCommand      `json:"commands"`
	Tokens        map[string]ModelTokens `json:"tokens"`
	Cost          float64                `json:"cost"`
}

type Session struct {
	Agent               string                 `json:"agent"`
	ID                  string                 `json:"id"`
	Name                *string                `json:"name"`
	Summary             *string                `json:"summary"`
	Status              *string                `json:"status"`
	WorkingDirectory    string                 `json:"cwd"`
	Branch              *string                `json:"branch"`
	Repository          *string                `json:"repo"`
	RepositoryLocalOnly bool                   `json:"repoLocalOnly"`
	EditedFileCount     int                    `json:"files"`
	DurationMs          *int                   `json:"durationMs"`
	Tokens              map[string]ModelTokens `json:"tokens"`
	Cost                float64                `json:"cost"`
	UnpricedModels      []string               `json:"unpricedModels"`
	Subagents           []Subagent             `json:"subagents"`
	StartedAt           int64                  `json:"-"`
	LastActivityTime    int64                  `json:"mtime"`
	Entrypoint          *string                `json:"entrypoint"`
	SessionDetails
}

const (
	DigestFirstPrompt = "first_prompt"
	DigestUser        = "user"
	DigestQuestion    = "question"
	DigestTodos       = "todos"
	DigestCompaction  = "compaction"
	DigestRecap       = "recap"
	DigestSubagent    = "subagent"
)

// A DigestEntry's position in Session.Digest is its position in the transcript
type DigestEntry struct {
	Turn        int    `json:"turn"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Answer      string `json:"answer,omitempty"`
	SubagentID  string `json:"subagentId,omitempty"`
	SpawnKey    string `json:"-"`
}

type FileEdit struct {
	Path      string `json:"path"`
	Additions int    `json:"adds"`
	Deletions int    `json:"dels"`
	Edits     int    `json:"edits"`
	IsNew     bool   `json:"isNew"`
	changes   []FileChange
}

type GitDrift struct {
	BaseBranch string `json:"baseBranch"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
}

type Todo struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type SessionSynthesis struct {
	Goals        []string `json:"goals"`
	Outcome      string   `json:"outcome"`
	KeyDecisions []string `json:"keyDecisions"`
	NextStep     string   `json:"nextStep"`
}

type SessionDetails struct {
	Model            *string           `json:"model"`
	ContextTokens    *int              `json:"contextTokens"`
	ContextWindow    *int              `json:"contextWindow"`
	Turns            int               `json:"turns"`
	ToolUses         int               `json:"toolUses"`
	Errors           int               `json:"errors"`
	Compactions      int               `json:"compactions"`
	FirstPrompt      *string           `json:"firstPrompt"`
	Commands         []string          `json:"commands"`
	Commits          []string          `json:"commits"`
	PullRequests     int               `json:"prs"`
	Todos            []Todo            `json:"todos"`
	Digest           []DigestEntry     `json:"digest"`
	FileEdits        []FileEdit        `json:"fileEdits"`
	Git              *GitDrift         `json:"git"`
	GitProbed        bool              `json:"-"`
	LastEditAt       *int64            `json:"lastEditAt"`
	Synthesis        *SessionSynthesis `json:"synthesis"`
	SynthesisPending bool              `json:"synthesisPending"`
	DeclaredGoal     *string           `json:"declaredGoal"`
	CompactionSeed   string            `json:"-"`
}
