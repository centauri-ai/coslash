package hubclient

import (
	"bytes"
	"context"
	"os/exec"
	"syscall"
)

func credentialCommand(ctx context.Context, store OSKeychain, credential string) (*exec.Cmd, error) {
	return darwinCredentialCommand(ctx, store, credential), nil
}

func darwinCredentialCommand(ctx context.Context, store OSKeychain, credential string) *exec.Cmd {
	command := exec.CommandContext(ctx, "/usr/bin/security", "add-generic-password", "-U", "-s", store.Service, "-a", store.Account, "-w")
	command.Stdin = darwinCredentialInput(credential)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return command
}

func darwinCredentialInput(credential string) *bytes.Buffer {
	return bytes.NewBufferString(credential + "\n" + credential + "\n")
}
