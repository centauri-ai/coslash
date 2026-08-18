package hubclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

var ErrNotPaired = errors.New("hub device credential is not available")

type CredentialStore interface {
	Load(context.Context) (string, error)
	Save(context.Context, string) error
	Delete(context.Context) error
}

type OSKeychain struct {
	Service string
	Account string
}

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
		return "", ErrNotPaired
	}
	credential := strings.TrimSpace(string(output))
	if credential == "" {
		return "", ErrNotPaired
	}
	return credential, nil
}

func (s OSKeychain) Save(ctx context.Context, credential string) error {
	if strings.TrimSpace(credential) == "" {
		return errors.New("save Hub credential: empty credential")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "/usr/bin/security", "add-generic-password", "-U", "-s", s.Service, "-a", s.Account, "-w", credential)
	case "linux":
		command = exec.CommandContext(ctx, "secret-tool", "store", "--label=coSlash Hub device", "service", s.Service, "account", s.Account)
		command.Stdin = bytes.NewBufferString(credential)
	default:
		return fmt.Errorf("save Hub credential: unsupported OS %s", runtime.GOOS)
	}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("save Hub credential: keychain command failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s OSKeychain) Delete(ctx context.Context) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "/usr/bin/security", "delete-generic-password", "-s", s.Service, "-a", s.Account)
	case "linux":
		command = exec.CommandContext(ctx, "secret-tool", "clear", "service", s.Service, "account", s.Account)
	default:
		return nil
	}
	if err := command.Run(); err != nil {
		return ErrNotPaired
	}
	return nil
}
