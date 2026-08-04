package diagnostics

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const maxVersionLength = 64

func commandVersion(ctx context.Context, bin string) string {
	versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(versionCtx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	version := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if len(version) > maxVersionLength {
		version = version[:maxVersionLength]
	}
	return version
}
