package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

var (
	lookPath = exec.LookPath
	chdir    = os.Chdir
	runAgent = runAgentProcess
)

// Execute resolves availability, changes to the session directory, and starts the agent.
// workingDirectory must already come from local Linux session lookup; it is never shelled.
func Execute(agent, workingDirectory, sessionID, mode, handoffID string) error {
	if err := ValidateRemoteAgent(agent); err != nil {
		return err
	}
	if err := ValidateUUIDSessionID(sessionID); err != nil {
		return err
	}
	if err := ValidateRemoteMode(mode, handoffID); err != nil {
		return err
	}
	if err := CleanupHandoffs(); err != nil {
		return err
	}
	if workingDirectory == "" {
		return fmt.Errorf("%w: session has no working directory", ErrWorkingDirectory)
	}
	info, err := os.Stat(workingDirectory)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWorkingDirectory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: not a directory", ErrWorkingDirectory)
	}
	if _, err := lookPath(agent); err != nil {
		return fmt.Errorf("%w: %s", ErrAgentUnavailable, agent)
	}

	var (
		handoffCleanup func() error
		promptPath     string
		handoffBody    string
	)
	if mode == NewSession {
		handoffBody, handoffCleanup, err = ClaimHandoff(agent, sessionID, handoffID)
		if err != nil {
			return err
		}
		defer func() {
			if promptPath != "" {
				_ = os.Remove(promptPath)
			}
			if handoffCleanup != nil {
				_ = handoffCleanup()
			}
		}()
		if agent == vendors.AgentClaude {
			promptPath, err = writePromptFile(handoffBody)
			if err != nil {
				return err
			}
		}
	}

	argv, err := agentArgv(agent, sessionID, mode, promptPath, handoffBody)
	if err != nil {
		return err
	}
	if err := chdir(workingDirectory); err != nil {
		return fmt.Errorf("%w: %v", ErrWorkingDirectory, err)
	}
	if err := runAgent(argv[0], argv[1:]); err != nil {
		return fmt.Errorf("%w: %v", ErrStartFailed, err)
	}
	return nil
}

func agentArgv(agent, sessionID, mode, promptPath, handoffBody string) ([]string, error) {
	switch mode {
	case ResumeSession:
		flag, err := resumeFlag(agent)
		if err != nil {
			return nil, err
		}
		return []string{agent, flag, sessionID}, nil
	case NewSession:
		switch agent {
		case vendors.AgentClaude:
			if promptPath == "" {
				return nil, fmt.Errorf("%w: missing handoff prompt file", ErrInvalidInput)
			}
			return []string{agent, "--append-system-prompt-file", promptPath}, nil
		case vendors.AgentCodex:
			encoded, err := json.Marshal(handoffBody)
			if err != nil {
				return nil, fmt.Errorf("launch: encoding handoff context: %w", err)
			}
			return []string{agent, "-c", "developer_instructions=" + string(encoded)}, nil
		default:
			return nil, fmt.Errorf("%w: unsupported agent %q", ErrInvalidInput, agent)
		}
	default:
		return nil, fmt.Errorf("%w: unknown mode %q", ErrInvalidInput, mode)
	}
}

func writePromptFile(body string) (string, error) {
	if err := ensureHandoffDir(); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(handoffDir(), "prompt-*")
	if err != nil {
		return "", fmt.Errorf("launch: creating prompt file: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(path)
		return "", fmt.Errorf("launch: securing prompt file: %w", err)
	}
	if _, err := file.WriteString(body); err != nil {
		file.Close()
		os.Remove(path)
		return "", fmt.Errorf("launch: writing prompt file: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("launch: closing prompt file: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		os.Remove(path)
		return "", fmt.Errorf("launch: resolving prompt path: %w", err)
	}
	return abs, nil
}
