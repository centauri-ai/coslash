package remote

import (
	"context"

	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

const (
	codexLivenessCommand  = "lsof -a -c codex -Fn"
	maxCodexLivenessBytes = 1 << 20
)

// lsof exits non-zero when nothing matches, which a failed SSH command looks
// the same as, so both return an empty set.
func (target sshTarget) codexLive(ctx context.Context) map[string]struct{} {
	output, err := target.run(ctx, codexLivenessCommand, maxCodexLivenessBytes, "")
	if err != nil {
		return map[string]struct{}{}
	}
	return codex.LiveSessionIDs(output)
}
