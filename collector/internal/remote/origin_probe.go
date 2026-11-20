package remote

import (
	"context"
	"errors"
	"io"
	"path"
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

// remoteGit is what one SSH probe reads in a working directory.
type remoteGit struct {
	origin, branch string
	checkout       *session.CheckoutObservation
}

func (target sshTarget) gitFacts(ctx context.Context, cwds []string) map[string]remoteGit {
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
		return map[string]remoteGit{}
	}
	output, err := target.run(ctx, "sh -c "+shellQuote(originProbeScript(len(safe))), maxOriginProbeBytes, strings.Join(safe, "\n")+"\n")
	if err != nil {
		return map[string]remoteGit{}
	}
	return parseOriginProbeOutput(safe, output)
}

// One line because csh and tcsh reject a newline inside the quotes for sh -c.
// Stripping control characters, tabs included, from the URL and branch stops
// one checkout from forging another index's record. Each framed row gets one
// share of the response ceiling, with branch output yielding to the origin.
func originProbeScript(probeCount int) string {
	entryBudget := maxOriginProbeBytes / probeCount
	// Additional fields are emitted only for a registered checkout and within the same row budget.
	// The remote shell can be csh, so join the POSIX script onto one line.
	checkout := strings.Join(strings.FieldsFunc(`
extra=;
root=$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null);
if [ -d "$root" ]; then
 admin=$(git -C "$cwd" rev-parse --absolute-git-dir 2>/dev/null);
 common=$(git -C "$cwd" rev-parse --path-format=absolute --git-common-dir 2>/dev/null);
 marker=$common;
 if [ "$admin" != "$common" ]; then
  marker=$admin/commondir; back=$(head -c 4097 "$admin/gitdir" 2>/dev/null);
  case "$back" in /*) : ;; *) back=$admin/$back ;; esac;
  [ "$back" -ef "$root/.git" ] || marker=;
 fi;
 if [ -n "$marker" ] && [ -e "$marker" ]; then
  clone=$(stat -Lc '%d:%i:%W' "$common" 2>/dev/null || stat -Lf '%d:%i:%B' "$common" 2>/dev/null);
  tree=$clone;
  if [ "$admin" != "$common" ]; then
   tree=$(stat -Lc '%d:%i:%W:%y' "$marker" 2>/dev/null || stat -Lf '%d:%i:%B:%Fm' "$marker" 2>/dev/null);
  fi;
  head=$(git -C "$cwd" rev-parse --verify HEAD 2>/dev/null);
  clean=$(printf '%s' "$root$admin$common$clone$tree$head" | tr -d '[:cntrl:]');
  if [ "$clean" = "$root$admin$common$clone$tree$head" ] && [ -n "$clone" ] && [ -n "$tree" ]; then
   extra=$(printf '\t%s\t%s\t%s\t%s\t%s\t%s' "$root" "$admin" "$common" "$clone" "$tree" "$head");
  fi;
 fi;
fi;
if [ "${#extra}" -gt "$((branch_limit - ${#branch}))" ]; then extra=; fi;
`, func(r rune) bool { return r == '\n' }), " ") + " "

	return `set +e; export GIT_TERMINAL_PROMPT=0 LC_ALL=C; entry_budget=` + strconv.Itoa(entryBudget) + `; i=0; while IFS= read -r cwd || [ -n "$cwd" ]; do url=$(git -C "$cwd" remote get-url origin 2>/dev/null | tr -d "[:cntrl:]" | head -c "$((entry_budget + 1))"); if [ "${#url}" -gt "$entry_budget" ]; then url=; fi; branch=$(git -C "$cwd" symbolic-ref --quiet --short HEAD 2>/dev/null | head -c 281 | tr -d "[:cntrl:]"); if [ "${#branch}" -gt 280 ]; then branch=; fi; entry_limit=$((entry_budget - ${#i} - 3)); if [ "${#url}" -gt "$entry_limit" ]; then url=; fi; branch_limit=$((entry_limit - ${#url})); if [ "${#branch}" -gt "$branch_limit" ]; then branch=; fi; ` + checkout + `printf "%s\t%s\t%s%s\n" "$i" "$url" "$branch" "$extra"; i=$((i + 1)); done`
}

func parseOriginProbeOutput(cwds []string, output string) map[string]remoteGit {
	found := map[string]remoteGit{}
	seen := make([]bool, len(cwds))
	for _, line := range strings.Split(output, "\n") {
		indexText, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(indexText))
		if err != nil || index < 0 || index >= len(cwds) || seen[index] {
			continue
		}
		seen[index] = true
		rawURL, rest, _ := strings.Cut(rest, "\t")
		branch, checkout, _ := strings.Cut(rest, "\t")
		facts := remoteGit{origin: session.CanonicalOriginURL(rawURL)}
		if branch = strings.TrimSpace(branch); branch != "" && len(branch) <= remotefacts.MaxDisplayBytes && utf8.ValidString(branch) && !strings.ContainsFunc(branch, unicode.IsControl) {
			facts.branch = branch
		}
		parts := strings.Split(checkout, "\t")
		if len(parts) == 6 {
			root, admin, common, clone, tree, head := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
			main := admin == common
			label := path.Base(root)
			if probeableGitCwd(root) && probeableGitCwd(admin) && probeableGitCwd(common) && len(label) <= 255 && len(clone) > 0 && len(clone) <= 100 && len(tree) > 0 && len(tree) <= 150 && (main || path.Dir(admin) == path.Join(common, "worktrees")) {
				kind := "unknown"
				if facts.branch != "" {
					kind = "branch"
					if head == "" {
						kind = "unborn"
					}
					head = facts.branch
				} else if len(head) == 40 || len(head) == 64 {
					kind = "detached"
				} else {
					head = ""
				}
				facts.checkout = &session.CheckoutObservation{Location: session.Location{Root: root, Label: label, Main: main, Repo: facts.origin, HeadKind: kind, Head: head, LastSeen: time.Now().UnixMilli(), Availability: "available"}, Common: common, Admin: admin, CloneKey: clone, CheckoutKey: tree}
			}
		}
		if facts != (remoteGit{}) {
			found[cwds[index]] = facts
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

// repairRemoteDisplay fills missing repositories and reads checkout facts
// from Git on the host, else the repository last read for that source and
// directory. It copies sessions before editing so a failed validation leaves
// the cached family unchanged.
func repairRemoteDisplay(ctx context.Context, snapshot *CachedSnapshotV2, sourceID string, lookup func(context.Context, []string) map[string]remoteGit) map[string]*session.CheckoutObservation {
	cwds := []string{}
	refresh := []string{}
	seen := map[string]bool{}
	for _, family := range snapshot.Families {
		for _, item := range family.Facts.Sessions {
			cwd := item.Display.WorkingDirectory
			if cwd == "" || seen[cwd] {
				continue
			}
			seen[cwd] = true
			if hasRepository(item.Display) && hasBranchName(item) {
				refresh = append(refresh, cwd)
			} else {
				cwds = append(cwds, cwd)
			}
		}
	}
	if len(cwds) < maxOriginProbes {
		cwds = append(cwds, refresh[:min(len(refresh), maxOriginProbes-len(cwds))]...)
	}
	if len(cwds) > maxOriginProbes {
		cwds = cwds[:maxOriginProbes]
	}
	if len(cwds) == 0 {
		return nil
	}
	probes := map[string]*session.CheckoutObservation{}
	found := lookup(ctx, cwds)
	for _, cwd := range cwds {
		probes[cwd] = found[cwd].checkout
		session.RememberRepository(sourceID+"\x00"+cwd, found[cwd].origin)
	}
	for index := range snapshot.Families {
		family := snapshot.Families[index].Facts
		changed := false
		for sessionIndex, item := range family.Sessions {
			cwd := item.Display.WorkingDirectory
			facts := found[cwd]
			if facts.origin == "" && cwd != "" {
				facts.origin = session.RememberedRepository(sourceID + "\x00" + cwd)
			}
			setRepository := facts.origin != "" && !hasRepository(item.Display)
			if !setRepository {
				continue
			}
			if !changed {
				family.Sessions = append([]remotefacts.Session(nil), family.Sessions...)
				changed = true
			}
			family.Sessions[sessionIndex].Display.Repository = &facts.origin
			family.Sessions[sessionIndex].Display.RepositoryLocalOnly = false
		}
		if changed && remotefacts.Validate(family) == nil {
			snapshot.Families[index].Facts = family
		}
	}
	return probes
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
