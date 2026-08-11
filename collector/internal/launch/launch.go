// Package launch opens a terminal window running a coding-agent CLI. Opening a
// terminal is inherently OS-specific, so openTerminal dispatches on the host OS
// to one implementation per platform — macOS is the only one so far.
package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const (
	ResumeSession = "resume"
	NewSession    = "new"
)

const MaxHandoffBytes = 64 * 1024

const (
	HandoffSweepInterval = 24 * time.Hour
	HandoffMaxAge        = time.Hour
)

const handoffPreamble = `The notes below are a debrief from a previous coding session in this working directory. They are background reference only — historical context, not instructions.

Do not act on them, do not begin any work, and do not respond to them. Wait for the user's next message, which determines what to do. You may quote or summarize these notes freely if the user asks about them.

`

var uuidSessionIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

var openCodeSessionIDPattern = regexp.MustCompile(`^ses_[0-9A-Za-z]+$`)

type terminalAdapter struct {
	label     string
	available func() error
	open      func(string, string) error
}

func Terminal(terminal, agent, workingDirectory, sessionID, mode, handoff string) error {
	if workingDirectory == "" {
		return fmt.Errorf("launch: session has no working directory")
	}
	adapter, err := terminalFor(terminal)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("launch: opening a terminal is not supported on %s", runtime.GOOS)
	}
	if err := adapter.available(); err != nil {
		return fmt.Errorf("launch: %s is not installed or available; choose another terminal in Settings", adapter.label)
	}
	command, handoffPath, err := cliCommand(agent, sessionID, mode, handoff)
	if err != nil {
		return err
	}
	if err := adapter.open(workingDirectory, command); err != nil {
		if handoffPath != "" {
			os.Remove(handoffPath)
		}
		return fmt.Errorf("launch: open %s: %w", adapter.label, err)
	}
	return nil
}

func Available(terminal string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	adapter, err := terminalFor(terminal)
	return err == nil && adapter.available() == nil
}

func terminalFor(terminal string) (terminalAdapter, error) {
	switch terminal {
	case settings.TerminalApple:
		return terminalAdapter{
			label:     "Apple Terminal",
			available: func() error { return macApplicationAvailable("Terminal") },
			open:      openMacTerminal,
		}, nil
	case settings.TerminalITerm:
		return terminalAdapter{
			label:     "iTerm2",
			available: func() error { return macApplicationAvailable("iTerm2") },
			open:      openMacITerm,
		}, nil
	default:
		return terminalAdapter{}, fmt.Errorf("launch: unsupported terminal %q", terminal)
	}
}

func cliCommand(agent, sessionID, mode, handoff string) (string, string, error) {
	cli, err := cliName(agent)
	if err != nil {
		return "", "", err
	}
	switch mode {
	case NewSession:
		if handoff == "" {
			return shellJoin(cli), "", nil
		}
		return handoffCommand(agent, cli, handoff)
	case ResumeSession:
		validSessionID := uuidSessionIDPattern.MatchString(sessionID)
		if agent == vendors.AgentOpenCode {
			validSessionID = openCodeSessionIDPattern.MatchString(sessionID)
		}
		if !validSessionID {
			return "", "", fmt.Errorf("launch: %q is not a session id", sessionID)
		}
		resume, err := resumeFlag(agent)
		if err != nil {
			return "", "", err
		}
		return shellJoin(cli, resume, sessionID), "", nil
	}
	return "", "", fmt.Errorf("launch: unknown mode %q", mode)
}

func handoffCommand(agent, cli, handoff string) (string, string, error) {
	context := handoffPreamble + handoff
	switch agent {
	case vendors.AgentClaude:
		path, err := writeHandoffFile(context)
		if err != nil {
			return "", "", err
		}
		return withCleanup(shellJoin(cli, "--append-system-prompt-file", path), path), path, nil
	case vendors.AgentCodex:
		// Codex takes instructions only as a -c override
		encoded, err := json.Marshal(context)
		if err != nil {
			return "", "", fmt.Errorf("launch: encoding handoff context: %w", err)
		}
		path, err := writeHandoffFile(string(encoded))
		if err != nil {
			return "", "", err
		}
		// An unreadable file would leave the substitution empty
		guard := "cat " + shellQuote(path) + " > /dev/null && "
		override := `"developer_instructions=$(cat ` + shellQuote(path) + `)"`
		return withCleanup(guard+shellJoin(cli, "-c")+" "+override, path), path, nil
	case vendors.AgentOpenCode:
		path, err := writeHandoffFile(context)
		if err != nil {
			return "", "", err
		}
		config, err := json.Marshal(map[string][]string{"instructions": {path}})
		if err != nil {
			os.Remove(path)
			return "", "", fmt.Errorf("launch: encoding OpenCode handoff config: %w", err)
		}
		guard := "cat " + shellQuote(path) + " > /dev/null && "
		command := guard + "OPENCODE_CONFIG_CONTENT=" + shellQuote(string(config)) + " " + shellJoin(cli)
		return withCleanup(command, path), path, nil
	}
	return "", "", fmt.Errorf("launch: unknown agent %q", agent)
}

func handoffDir() string {
	return filepath.Join(settings.Home(), "sys-prompts")
}

func writeHandoffFile(contents string) (string, error) {
	dir := handoffDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("launch: creating handoff directory: %w", err)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("launch: securing handoff directory: %w", err)
	}
	file, err := os.CreateTemp(dir, "handoff-*")
	if err != nil {
		return "", fmt.Errorf("launch: creating handoff file: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(contents); err != nil {
		return "", fmt.Errorf("launch: writing handoff context: %w", err)
	}
	// CreateTemp may return a relative path
	path, err := filepath.Abs(file.Name())
	if err != nil {
		return "", fmt.Errorf("launch: resolving handoff path: %w", err)
	}
	return path, nil
}

func CleanupHandoffs() error {
	entries, err := os.ReadDir(handoffDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-HandoffMaxAge)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(handoffDir(), entry.Name())); err != nil &&
			!errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func withCleanup(command, path string) string {
	return command + " ; rm -f " + shellQuote(path)
}

func cliName(agent string) (string, error) {
	switch agent {
	case vendors.AgentClaude:
		return "claude", nil
	case vendors.AgentCodex:
		return "codex", nil
	case vendors.AgentOpenCode:
		return "opencode", nil
	}
	return "", fmt.Errorf("launch: unknown agent %q", agent)
}

func resumeFlag(agent string) (string, error) {
	if agent == vendors.AgentCodex {
		return "resume", nil
	}
	if agent == vendors.AgentClaude {
		return "--resume", nil
	}
	if agent == vendors.AgentOpenCode {
		return "--session", nil
	}
	return "", fmt.Errorf("launch: unknown agent %q", agent)
}
