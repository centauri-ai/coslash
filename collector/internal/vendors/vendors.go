package vendors

import "runtime"

const (
	AgentPi       = "pi"
	AgentClaude   = "claude"
	AgentCodex    = "codex"
	AgentCursor   = "cursor"
	AgentGrok     = "grok"
	AgentOpenCode = "opencode"
)

func PiSupported() bool                  { return piSupportedOn(runtime.GOOS) }
func piSupportedOn(platform string) bool { return platform == "darwin" }
