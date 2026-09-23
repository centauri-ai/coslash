package remote

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

const (
	maxOriginProbes       = 64
	maxOriginProbeBytes   = 32 << 10
	originProbeSSHTimeout = 15 * time.Second
)

// probeableGitCwd reports whether cwd can be sent as one stdin line. The remote
// command is fixed; paths are not interpolated into the shell. Newlines and
// other controls would split that line protocol, so those checkouts stay
// unidentified.
func probeableGitCwd(cwd string) bool {
	if cwd == "" || len(cwd) > 4096 || !strings.HasPrefix(cwd, "/") || !utf8.ValidString(cwd) {
		return false
	}
	for _, r := range cwd {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// originLookup resolves canonical repository identities for working directories.
// The map contains only directories whose origin remote canonicalized.
type originLookup func(ctx context.Context, cwds []string) map[string]string

func remoteOriginLookup(alias string, options OpenOptions) originLookup {
	return func(ctx context.Context, cwds []string) map[string]string {
		return probeRemoteOrigins(ctx, alias, options, cwds)
	}
}

func probeRemoteOrigins(ctx context.Context, alias string, options OpenOptions, cwds []string) map[string]string {
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
	output, err := runOriginProbe(ctx, alias, options, safe)
	if err != nil {
		return map[string]string{}
	}
	return parseOriginProbeOutput(safe, output)
}

func runOriginProbe(ctx context.Context, alias string, options OpenOptions, cwds []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, originProbeSSHTimeout)
	defer cancel()
	args, err := sshCommandArgs(alias, int(options.Limits.withDefaults().ConnectTimeout.Seconds()), "sh -c "+shellQuote(originProbeScript))
	if err != nil {
		return "", err
	}
	return runSSHStdout(probeCtx, options, args, maxOriginProbeBytes, strings.NewReader(originProbeInput(cwds)))
}

// originProbeScript is one line because csh and tcsh reject a newline inside
// the single quotes that wrap it for sh -c. It reads one absolute path per
// stdin line and prints one sanitized origin line per path. Control characters
// in the URL are removed before the record separator, so one checkout cannot
// invent a record for another index.
const originProbeScript = `set +e; export GIT_TERMINAL_PROMPT=0; i=0; while IFS= read -r cwd || [ -n "$cwd" ]; do url=$(git -C "$cwd" remote get-url origin 2>/dev/null | tr -d "[:cntrl:]"); printf "%s\t%s\n" "$i" "$url"; i=$((i + 1)); done`

func originProbeInput(cwds []string) string {
	return strings.Join(cwds, "\n") + "\n"
}

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

func runSSHStdout(ctx context.Context, options OpenOptions, args []string, limit int, stdin io.Reader) (string, error) {
	bin := options.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	command := options.command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(ctx, bin, args...)
	configureProcessGroup(cmd)
	cmd.Stdin = stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr := &cappedStderr{limit: options.Limits.withDefaults().MaxStderrBytes, cancel: func() {}}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	buf, readErr := io.ReadAll(io.LimitReader(stdout, int64(limit+1)))
	overLimit := len(buf) > limit
	if overLimit || ctx.Err() != nil {
		terminateProcessGroup(cmd)
	}
	_, drainErr := io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if overLimit {
		return "", errors.New("origin probe output too long")
	}
	if readErr != nil {
		return "", readErr
	}
	if drainErr != nil {
		return "", drainErr
	}
	if waitErr != nil {
		return "", waitErr
	}
	return string(buf), nil
}

func repairRemoteDisplayOverSSH(ctx context.Context, alias string, options OpenOptions, snapshot *CachedSnapshotV2) {
	if snapshot == nil || options.command != nil || alias == "" {
		return
	}
	repairRemoteDisplay(ctx, snapshot, remoteOriginLookup(alias, options))
}

// repairRemoteDisplay sets a canonical origin on sessions that recorded a branch.
// Sessions are copied first so a failed validation leaves the cached family unchanged.
func repairRemoteDisplay(ctx context.Context, snapshot *CachedSnapshotV2, lookup originLookup) {
	if snapshot == nil || lookup == nil {
		return
	}
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
		family.Sessions = append([]remotefacts.Session(nil), family.Sessions...)
		changed := false
		for sessionIndex := range family.Sessions {
			item := &family.Sessions[sessionIndex]
			name := origins[item.Display.WorkingDirectory]
			if name == "" || hasRepository(item.Display) {
				continue
			}
			item.Display.Repository = &name
			item.Display.RepositoryLocalOnly = false
			changed = true
		}
		if !changed {
			continue
		}
		if err := remotefacts.Validate(family); err != nil {
			continue
		}
		snapshot.Families[index].Facts = family
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
