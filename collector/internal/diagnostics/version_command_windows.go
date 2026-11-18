//go:build windows

package diagnostics

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/backgroundexec"
)

func versionCommand(ctx context.Context, bin string) *exec.Cmd {
	if strings.EqualFold(filepath.Ext(bin), ".ps1") {
		return backgroundexec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", bin, "--version")
	}
	return backgroundexec.CommandContext(ctx, bin, "--version")
}
