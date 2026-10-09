//go:build !windows

package hubclient

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

func (s OSKeychain) Load(ctx context.Context) (string, error) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", s.Service, "-a", s.Account, "-w")
	case "linux":
		command = exec.CommandContext(ctx, "secret-tool", "lookup", "service", s.Service, "account", s.Account)
	default:
		return "", fmt.Errorf("load Hub credential: unsupported OS %s", runtime.GOOS)
	}
	output, err := command.Output()
	if err != nil {
		return "", keychainCommandLoadError(ctx)
	}
	credential := strings.TrimSpace(string(output))
	if credential == "" {
		return "", ErrNotPaired
	}
	return credential, nil
}

func keychainCommandLoadError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrNotPaired
}

func (s OSKeychain) save(ctx context.Context, credential string) error {
	if strings.TrimSpace(credential) == "" {
		return errors.New("save Hub credential: empty credential")
	}
	command, err := credentialCommand(ctx, s, credential)
	if err != nil {
		return err
	}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("save Hub credential: keychain command failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s OSKeychain) delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "/usr/bin/security", "delete-generic-password", "-s", s.Service, "-a", s.Account)
	case "linux":
		command = exec.CommandContext(ctx, "secret-tool", "clear", "service", s.Service, "account", s.Account)
	default:
		return fmt.Errorf("delete Hub credential: unsupported OS %s", runtime.GOOS)
	}
	output, err := command.CombinedOutput()
	if err == nil || missingCredentialItem(string(output)) {
		return nil
	}
	return fmt.Errorf("delete Hub credential: keychain command failed: %w", err)
}

func missingCredentialItem(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "could not be found") || strings.Contains(message, "no matching items") || strings.Contains(message, "not found")
}
