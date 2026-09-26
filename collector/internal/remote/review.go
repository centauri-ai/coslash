package remote

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const remoteReviewOutputLimit = 32 << 10

type reviewOutput struct {
	bytes.Buffer
	truncated bool
}

func (output *reviewOutput) Write(data []byte) (int, error) {
	length := len(data)
	remaining := max(0, remoteReviewOutputLimit-output.Len())
	if length > remaining {
		output.truncated = true
	}
	_, _ = output.Buffer.Write(data[:min(length, remaining)])
	return length, nil
}

func remoteReviewCommand(reviewer, directory string) (string, error) {
	if directory == "" || strings.ContainsRune(directory, 0) {
		return "", fmt.Errorf("remote review working directory is required")
	}
	var args []string
	switch reviewer {
	case "claude":
		args = []string{"claude", "-p", "--permission-mode", "plan", "--safe-mode", "--strict-mcp-config", "--disable-slash-commands", "--tools", "Read,Glob,Grep"}
	case "codex":
		args = []string{"codex", "exec", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--sandbox", "read-only", "--skip-git-repo-check", "-"}
	default:
		return "", fmt.Errorf("unsupported remote reviewer %q", reviewer)
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	setup := `cd ` + shellQuote(directory) + ` || exit 1; PATH="$HOME/.local/bin:$PATH"; export PATH; `
	if reviewer == "claude" {
		setup += `if [ -f "$HOME/.agent-keys.sh" ]; then . "$HOME/.agent-keys.sh" || exit 1; fi; `
	}
	return setup + strings.Join(quoted, " "), nil
}

func AvailableReviewers(ctx context.Context, alias string) (map[string]bool, error) {
	return availableReviewers(ctx, alias, OpenOptions{})
}

func availableReviewers(ctx context.Context, alias string, options OpenOptions) (map[string]bool, error) {
	limits := options.Limits.withDefaults()
	args, err := handoffSSHArgs(alias, `PATH="$HOME/.local/bin:$PATH"; export PATH; for cli in claude codex; do command -v "$cli" >/dev/null 2>&1 && printf '%s\n' "$cli"; done`, int(limits.ConnectTimeout.Seconds()))
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, DefaultCapabilityTimeout)
	defer cancel()
	if options.command == nil {
		if err := ensureControlMaster(runCtx, alias, options); err != nil {
			return nil, err
		}
	}
	bin := options.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	commandContext := options.command
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	cmd := commandContext(runCtx, bin, args...)
	cmd.WaitDelay = 5 * time.Second
	output := &reviewOutput{}
	cmd.Stdout = output
	stderr := &cappedStderr{limit: limits.MaxStderrBytes, cancel: cancel}
	cmd.Stderr = stderr
	if err := startProcessGroup(cmd); err != nil {
		return nil, err
	}
	err = waitProcessGroupContext(runCtx, cmd)
	if runCtx.Err() != nil {
		return nil, runCtx.Err()
	}
	if err != nil {
		return nil, wrapSSHError(err, stderr.String())
	}
	if output.truncated || stderr.overflow {
		return nil, ErrStderrLimit
	}
	available := map[string]bool{}
	for _, line := range strings.Split(output.String(), "\n") {
		if line == "claude" || line == "codex" {
			available[line] = true
		}
	}
	return available, nil
}

func Review(ctx context.Context, alias, reviewer, workingDirectory, name, prompt string) (string, error) {
	return remoteReview(ctx, alias, reviewer, workingDirectory, name, prompt, OpenOptions{})
}

func remoteReview(ctx context.Context, alias, reviewer, workingDirectory, _ string, prompt string, options OpenOptions) (string, error) {
	command, err := remoteReviewCommand(reviewer, workingDirectory)
	if err != nil {
		return "", err
	}
	limits := options.Limits.withDefaults()
	args, err := handoffSSHArgs(alias, command, int(limits.ConnectTimeout.Seconds()))
	if err != nil {
		return "", err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if options.command == nil {
		if err := ensureControlMaster(runCtx, alias, options); err != nil {
			return "", err
		}
	}
	bin := options.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	commandContext := options.command
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	cmd := commandContext(runCtx, bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	stdout := &reviewOutput{}
	cmd.Stdout = stdout
	stderr := &cappedStderr{limit: limits.MaxStderrBytes, cancel: cancel}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	if err := startProcessGroup(cmd); err != nil {
		return "", err
	}
	err = waitProcessGroupContext(runCtx, cmd)
	if stderr.overflow {
		return "", ErrStderrLimit
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", wrapSSHError(err, stderr.String())
	}
	result := strings.TrimSpace(strings.ToValidUTF8(stdout.String(), "�"))
	if stdout.truncated {
		result += "\n[review output truncated]"
	}
	return result, nil
}
