//go:build !windows

package launch

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func localCommandJoin(arguments ...string) string {
	return shellJoin(arguments...)
}

func localCommandWithEnv(name, value string, arguments ...string) string {
	return name + "=" + shellQuote(value) + " " + shellJoin(arguments...)
}

func terminalSSHOptions() []string {
	return []string{"-o", "ControlMaster=auto", "-o", "ControlPath=" + settings.SSHControlPath()}
}

func handoffCommand(agent, cli, handoff, prompt string) (string, string, error) {
	context := handoffPreamble + handoff
	switch agent {
	case vendors.AgentClaude:
		path, err := writeHandoffFile(context)
		if err != nil {
			return "", "", err
		}
		arguments := []string{cli, "--append-system-prompt-file", path}
		if prompt != "" {
			arguments = append(arguments, "--", prompt)
		}
		return withCleanup(shellJoin(arguments...), path), path, nil
	case vendors.AgentCodex:
		encoded, err := json.Marshal(context)
		if err != nil {
			return "", "", fmt.Errorf("launch: encoding handoff context: %w", err)
		}
		path, err := writeHandoffFile(string(encoded))
		if err != nil {
			return "", "", err
		}
		guard := "cat " + shellQuote(path) + " > /dev/null && "
		override := `"developer_instructions=$(cat ` + shellQuote(path) + `)"`
		command := guard + shellJoin(cli, "-c") + " " + override
		if prompt != "" {
			command += " " + shellJoin("--", prompt)
		}
		return withCleanup(command, path), path, nil
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
		if prompt != "" {
			command += " " + shellQuote(prompt)
		}
		return withCleanup(command, path), path, nil
	case vendors.AgentCursor:
		return shellJoin(cli), "", nil
	}
	return "", "", fmt.Errorf("launch: unknown agent %q", agent)
}

func withCleanup(command, path string) string {
	return command + " ; rm -f " + shellQuote(path)
}
