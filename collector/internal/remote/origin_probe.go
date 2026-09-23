package remote

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

const (
	maxOriginProbes     = 64
	maxOriginProbeBytes = 32 << 10
	sshProbeTimeout     = 15 * time.Second
)

func probeableGitCwd(cwd string) bool {
	return strings.HasPrefix(cwd, "/") && len(cwd) <= 4096 && utf8.ValidString(cwd) && strings.IndexFunc(cwd, unicode.IsControl) < 0
}

type sshTarget struct {
	alias   string
	options OpenOptions
}

func (target sshTarget) origins(ctx context.Context, cwds []string) map[string]string {
	safe := make([]string, 0, min(len(cwds), maxOriginProbes))
	for _, cwd := range cwds {
		if len(safe) == maxOriginProbes {
			break
		}
		if probeableGitCwd(cwd) {
			safe = append(safe, cwd)
		}
	}
	if len(safe) == 0 {
		return map[string]string{}
	}
	output, err := target.run(ctx, "sh -c "+shellQuote(originProbeScript), maxOriginProbeBytes, strings.Join(safe, "\n")+"\n")
	if err != nil {
		return map[string]string{}
	}
	return parseOriginProbeOutput(safe, output)
}

// One line because csh and tcsh reject a newline inside the quotes for sh -c.
// Stripping control characters from the URL stops one checkout from forging
// another index's record.
const originProbeScript = `set +e; export GIT_TERMINAL_PROMPT=0; i=0; while IFS= read -r cwd || [ -n "$cwd" ]; do url=$(git -C "$cwd" remote get-url origin 2>/dev/null | tr -d "[:cntrl:]"); printf "%s\t%s\n" "$i" "$url"; i=$((i + 1)); done`

func parseOriginProbeOutput(cwds []string, output string) map[string]string {
	found := map[string]string{}
	seen := make([]bool, len(cwds))
	for _, line := range strings.Split(output, "\n") {
		indexText, rawURL, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(indexText))
		if err != nil || index < 0 || index >= len(cwds) || seen[index] {
			continue
		}
		seen[index] = true
		if name := session.CanonicalOriginURL(rawURL); name != "" {
			found[cwds[index]] = name
		}
	}
	return found
}

func (target sshTarget) run(ctx context.Context, remoteCommand string, limit int, stdin string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, sshProbeTimeout)
	defer cancel()
	args, err := sshCommandArgs(target.alias, int(target.options.Limits.withDefaults().ConnectTimeout.Seconds()), remoteCommand)
	if err != nil {
		return "", err
	}
	process, err := startHelper(runCtx, target.alias, args, cancel, target.options)
	if err != nil {
		return "", err
	}
	written := process.writeStdin([]byte(stdin))
	buf, readErr := io.ReadAll(io.LimitReader(process.stdout, int64(limit+1)))
	overLimit := len(buf) > limit
	_, waitErr := process.finish(overLimit || readErr != nil)
	<-written
	switch {
	case runCtx.Err() != nil:
		return "", runCtx.Err()
	case overLimit:
		return "", errors.New("SSH probe output too long")
	case readErr != nil:
		return "", readErr
	case waitErr != nil:
		return "", waitErr
	}
	return string(buf), nil
}

// repairRemoteDisplay copies sessions before editing so a failed validation
// leaves the cached family unchanged.
func repairRemoteDisplay(ctx context.Context, snapshot *CachedSnapshotV2, lookup func(context.Context, []string) map[string]string) {
	cwds := []string{}
	seen := map[string]bool{}
	for _, family := range snapshot.Families {
		for _, item := range family.Facts.Sessions {
			cwd := item.Display.WorkingDirectory
			if cwd == "" || hasRepository(item.Display) || !hasBranchName(item) || seen[cwd] {
				continue
			}
			seen[cwd] = true
			cwds = append(cwds, cwd)
		}
	}
	if len(cwds) == 0 {
		return
	}
	origins := lookup(ctx, cwds)
	for index := range snapshot.Families {
		family := snapshot.Families[index].Facts
		changed := false
		for sessionIndex, item := range family.Sessions {
			name := origins[item.Display.WorkingDirectory]
			if name == "" || hasRepository(item.Display) {
				continue
			}
			if !changed {
				family.Sessions = append([]remotefacts.Session(nil), family.Sessions...)
				changed = true
			}
			family.Sessions[sessionIndex].Display.Repository = &name
			family.Sessions[sessionIndex].Display.RepositoryLocalOnly = false
		}
		if changed && remotefacts.Validate(family) == nil {
			snapshot.Families[index].Facts = family
		}
	}
}

func hasRepository(item session.Session) bool {
	return item.Repository != nil && strings.TrimSpace(*item.Repository) != ""
}

func hasBranchName(item remotefacts.Session) bool {
	if strings.TrimSpace(item.Branch) != "" {
		return true
	}
	return item.Display.Branch != nil && strings.TrimSpace(*item.Display.Branch) != ""
}
