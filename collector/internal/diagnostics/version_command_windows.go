//go:build windows

package diagnostics

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

func versionCommand(ctx context.Context, bin string) *exec.Cmd {
	if strings.EqualFold(filepath.Ext(bin), ".ps1") {
		return exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", bin, "--version")
	}
	return exec.CommandContext(ctx, bin, "--version")
}
