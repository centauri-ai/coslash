//go:build !darwin

package hubclient

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func credentialCommand(ctx context.Context, store OSKeychain, credential string) (*exec.Cmd, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("save Hub credential: unsupported OS %s", runtime.GOOS)
	}
	command := exec.CommandContext(ctx, "secret-tool", "store", "--label=coSlash Hub device", "service", store.Service, "account", store.Account)
	command.Stdin = bytes.NewBufferString(credential)
	return command, nil
}
