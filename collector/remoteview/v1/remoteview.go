// Package remoteviewv1 defines the private remote-session-view/v1 wire contract.
package remoteviewv1

const (
	SchemaVersion            = "remote-session-view/v1"
	CapabilityRemoteView     = "remote-session-view/v1"
	CapabilityRemoteLaunch   = "remote-launch/v1"
	MaxPayloadBytes          = 8 << 20
	MaxCollectorVersionBytes = 64
	MaxCapabilityBytes       = 64
	MaxCapabilities          = 32
	MaxLaunchableAgents      = 8
	MaxAgentBytes            = 64
	MaxIdentifierBytes       = 512
	MaxRepositoryBytes       = 1024
	MaxNameBytes             = 512
	MaxSummaryBytes          = 4 * 1024
	MaxPathBytes             = 1024
	MaxBranchBytes           = 512
	MaxEntrypointBytes       = 512
	MaxModelBytes            = 256
	MaxGoalBytes             = 16 * 1024
	MaxPromptBytes           = 16 * 1024
	MaxDigestItems           = 200
	MaxDigestTextBytes       = 4 * 1024
	MaxTodoItems             = 200
	MaxTodoTextBytes         = 2 * 1024
	MaxFileEditItems         = 2000
	MaxCommitItems           = 200
	MaxCommitTextBytes       = 2 * 1024
	MaxSubagentItems         = 100
	MaxSubagentTextBytes     = 8 * 1024
	MaxCommandLabelItems     = 200
	MaxCommandLabelBytes     = 512
	MaxUnpricedModels        = 100
	MaxUsageModels           = 100
	MaxSessions              = 1024
	TruncationReasonSession  = "session_limit"
	TruncationReasonPayload  = "payload_limit"
	FrameMagic               = "COSLASH-REMOTE/1"
	AgentClaude              = "claude"
	AgentCodex               = "codex"
)

// MaxSessionsPerAgent caps root transcripts discovered per agent before full
// parsing. Chosen from large-fixture measurement evidence in measurement_test.go.
const MaxSessionsPerAgent = 150

type View struct {
	SchemaVersion    string    `json:"schemaVersion"`
	CollectorVersion string    `json:"collectorVersion"`
	Capabilities     []string  `json:"capabilities"`
	LaunchableAgents []string  `json:"launchableAgents"`
	RequestedSinceMs int64     `json:"requestedSinceMs"`
	RequestNowMs     int64     `json:"requestNowMs"`
	HostNowMs        int64     `json:"hostNowMs"`
	CollectedAtMs    int64     `json:"collectedAtMs"`
	CoverageSinceMs  int64     `json:"coverageSinceMs"`
	Truncated        bool      `json:"truncated"`
	TruncationReason *string   `json:"truncationReason,omitempty"`
	Host             Host      `json:"host"`
	Sessions         []Session `json:"sessions"`
}

type Probe struct {
	SchemaVersion    string   `json:"schemaVersion"`
	CollectorVersion string   `json:"collectorVersion"`
	Capabilities     []string `json:"capabilities"`
	LaunchableAgents []string `json:"launchableAgents"`
	HostNowMs        int64    `json:"hostNowMs"`
	Host             Host     `json:"host"`
}

type Host struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type Session struct {
	Agent               string     `json:"agent"`
	SourceSessionID     string     `json:"sourceSessionId"`
	Name                *string    `json:"name,omitempty"`
	Summary             *string    `json:"summary,omitempty"`
	Status              *string    `json:"status,omitempty"`
	WorkingDirectory    *string    `json:"cwd,omitempty"`
	Repository          *string    `json:"repository,omitempty"`
	RepositoryLocalOnly bool       `json:"repositoryLocalOnly"`
	Branch              *string    `json:"branch,omitempty"`
	Entrypoint          *string    `json:"entrypoint,omitempty"`
	SessionStartedAtMs  int64      `json:"sessionStartedAtMs"`
	LastActivityAtMs    int64      `json:"lastActivityAtMs"`
	LastEditAtMs        *int64     `json:"lastEditAtMs,omitempty"`
	DurationMs          *int       `json:"durationMs,omitempty"`
	Model               *string    `json:"model,omitempty"`
	ContextTokens       *int       `json:"contextTokens,omitempty"`
	ContextWindow       *int       `json:"contextWindow,omitempty"`
	DeclaredGoal        *string    `json:"declaredGoal,omitempty"`
	FirstPrompt         *string    `json:"firstPrompt,omitempty"`
	Counts              Counts     `json:"counts"`
	Usage               Usage      `json:"usage"`
	Digest              []Digest   `json:"digest"`
	Todos               []Todo     `json:"todos"`
	FileEdits           []FileEdit `json:"fileEdits"`
	Commits             []string   `json:"commits"`
	Git                 *GitDrift  `json:"git,omitempty"`
	Subagents           []Subagent `json:"subagents"`
}

type Counts struct {
	EditedFiles  int `json:"editedFiles"`
	Turns        int `json:"turns"`
	ToolUses     int `json:"toolUses"`
	Errors       int `json:"errors"`
	Compactions  int `json:"compactions"`
	Commands     int `json:"commands"`
	PullRequests int `json:"pullRequests"`
}

type Usage struct {
	Models                []ModelUsage `json:"models"`
	EstimatedCostMicroUSD int64        `json:"estimatedCostMicroUsd"`
	UnpricedModels        []string     `json:"unpricedModels"`
}

type ModelUsage struct {
	Model                      string `json:"model"`
	InputTokens                int    `json:"inputTokens"`
	OutputTokens               int    `json:"outputTokens"`
	CacheCreationInputTokens   int    `json:"cacheCreationInputTokens"`
	CacheCreation1hInputTokens int    `json:"cacheCreation1hInputTokens"`
	CacheReadInputTokens       int    `json:"cacheReadInputTokens"`
	EstimatedCostMicroUSD      int64  `json:"estimatedCostMicroUsd"`
}

type Digest struct {
	Turn        int     `json:"turn"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Answer      *string `json:"answer,omitempty"`
	SubagentID  *string `json:"subagentId,omitempty"`
}

type Todo struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type FileEdit struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Edits     int    `json:"edits"`
	IsNew     bool   `json:"isNew"`
}

type GitDrift struct {
	BaseBranch string `json:"baseBranch"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
}

type Subagent struct {
	ID                    string       `json:"id"`
	Name                  string       `json:"name"`
	Model                 *string      `json:"model,omitempty"`
	Status                string       `json:"status"`
	Task                  string       `json:"task"`
	Result                string       `json:"result"`
	DurationMs            *int         `json:"durationMs,omitempty"`
	SpawnedAtTurn         *int         `json:"spawnedAtTurn,omitempty"`
	ToolUses              int          `json:"toolUses"`
	CommandLabels         []string     `json:"commandLabels"`
	Usage                 []ModelUsage `json:"usage"`
	EstimatedCostMicroUSD int64        `json:"estimatedCostMicroUsd"`
}

// LinuxCutoffMs converts a Mac request window into a Linux activity cutoff.
// A zero requestedSince means bounded full history.
func LinuxCutoffMs(hostNowMs, requestedSinceMs, requestNowMs int64) int64 {
	if requestedSinceMs == 0 {
		return 0
	}
	return hostNowMs - (requestNowMs - requestedSinceMs)
}
