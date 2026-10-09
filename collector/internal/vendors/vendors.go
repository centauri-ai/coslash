package vendors

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	AgentPi       = "pi"
	AgentClaude   = "claude"
	AgentCodex    = "codex"
	AgentCursor   = "cursor"
	AgentGrok     = "grok"
	AgentOpenCode = "opencode"
)

func PiSupported() bool                  { return piSupportedOn(runtime.GOOS) }
func piSupportedOn(platform string) bool { return platform == "darwin" || platform == "windows" }

// PiCollectionSupported covers session collection and the runtime extension,
// which also run on Linux hosts; Pi launch and synthesis stay on macOS and
// Windows.
func PiCollectionSupported() bool { return piCollectionSupportedOn(runtime.GOOS) }
func piCollectionSupportedOn(platform string) bool {
	return piSupportedOn(platform) || platform == "linux"
}

func PiExecutable() (string, error) {
	if path, err := exec.LookPath("pi"); err == nil {
		return path, nil
	} else if runtime.GOOS != "windows" {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return exec.LookPath(filepath.Join(home, ".pi", "agent", "bin", "pi.cmd"))
}

// GrokSynthesisSupported reports whether the Grok CLI may run synthesis.
// Session collection is separate from CLI execution.
func GrokSynthesisSupported() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "windows" }
