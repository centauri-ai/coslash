package remote

import (
	"context"

	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

const (
	// A pipe and one single-quoted argument parse the same in every common login
	// shell, and only rollout paths cross SSH.
	codexLivenessCommand  = `lsof -a -c codex -Fn | grep -E '/\.codex/(archived_)?sessions/(.*/)?rollout-[^/]*\.jsonl$'`
	maxCodexLivenessBytes = 1 << 20
)

// grep exits non-zero when nothing matches, which a failed SSH command looks
// the same as, so both return an empty set.
func (target sshTarget) codexLive(ctx context.Context) map[string]struct{} {
	output, err := target.run(ctx, codexLivenessCommand, maxCodexLivenessBytes, "")
	if err != nil {
		return map[string]struct{}{}
	}
	return codex.LiveSessionIDs(output)
}
