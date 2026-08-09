package revision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Preflight is everything a run needs to know about the user's repository
// before any run root exists.
type Preflight struct {
	// Toplevel is the canonical worktree root. It is always a realpath: on
	// macOS /tmp is a symlink to /private/tmp, git reports the real path in some
	// places and the given path in others, and a stored un-normalized path will
	// never match its own repository again.
	Toplevel string
	// BaseBranch is the branch the run starts from.
	BaseBranch string
	// BaseSha is the commit the run is authorized against. Empty for a plain
	// (non-git) folder until a run root snapshots the copy.
	BaseSha string
	// RemoteURL is origin's URL, never the remote name: a name resolves through
	// config that an agent inside the run root can rewrite, so publication must
	// already hold the URL it was authorized against.
	RemoteURL string
	// DefaultBranch is the repository default (origin/HEAD), for display and as
	// the publication target. Not automatically the run base — a linked worktree
	// is usually on a feature branch, and starting from the default would drop
	// that tip on the floor.
	DefaultBranch string
	// CheckoutBranch is the branch checked out at the selected path, empty when
	// HEAD is detached.
	CheckoutBranch string
	// IsLinkedWorktree reports git-dir != git-common-dir.
	IsLinkedWorktree bool
	// IsGitRepository is false for an ordinary folder, where the run root is a
	// copy rather than a clone.
	IsGitRepository bool
}

// PreflightOptions selects the project path and, optionally, an explicit base
// branch. An empty BaseBranch resolves the checked-out branch, then the
// repository default.
type PreflightOptions struct {
	ProjectPath string
	BaseBranch  string
	// AllowPlainFolder permits a directory that is not a git repository. Atlas
	// supports plain folders; DaGama does not.
	AllowPlainFolder bool
}

// operationMarkers name the in-progress states that make "what is the base of
// this run?" unanswerable. Refusing is better than guessing, because guessing
// wrongly produces a pull request against something the user did not intend.
var operationMarkers = []struct {
	marker  string
	message string
}{
	{"MERGE_HEAD", "a merge is in progress"},
	{"rebase-merge", "a rebase is in progress"},
	{"rebase-apply", "a rebase is in progress"},
	{"CHERRY_PICK_HEAD", "a cherry-pick is in progress"},
	{"REVERT_HEAD", "a revert is in progress"},
	{"BISECT_LOG", "a bisect is in progress"},
}

// RunPreflight inspects the user's repository without modifying it.
func (g *Git) RunPreflight(ctx context.Context, options PreflightOptions) (Preflight, error) {
	if options.ProjectPath == "" || !filepath.IsAbs(options.ProjectPath) {
		return Preflight{}, newError(CodeInvalidPath, "the project path must be an absolute path")
	}
	projectPath, err := filepath.EvalSymlinks(options.ProjectPath)
	if err != nil {
		return Preflight{}, newError(CodeInvalidPath, "the project folder does not exist").
			withDetail(err.Error()).withCause(err)
	}

	// Bareness is checked before the toplevel: a bare repository has no worktree
	// and --show-toplevel simply fails there. Reporting "not a git repository"
	// for a repository that plainly is one sends the user looking in entirely the
	// wrong place.
	bare, ok := g.Try(ctx, Command{Args: []string{"rev-parse", "--is-bare-repository"}, Dir: projectPath})
	if !ok {
		if options.AllowPlainFolder {
			return g.plainFolderPreflight(projectPath)
		}
		return Preflight{}, newError(CodeNotARepository, "the project folder is not inside a git repository")
	}
	if bare == "true" {
		return Preflight{}, newError(CodeBareRepository, "a run cannot be started from a bare repository")
	}

	toplevelRaw, ok := g.Try(ctx, Command{Args: []string{"rev-parse", "--show-toplevel"}, Dir: projectPath})
	if !ok || toplevelRaw == "" {
		return Preflight{}, newError(CodeNoWorktree, "the project folder has no git worktree")
	}
	toplevel, err := filepath.EvalSymlinks(toplevelRaw)
	if err != nil {
		return Preflight{}, newError(CodeNoWorktree, "the git worktree could not be resolved").
			withDetail(err.Error()).withCause(err)
	}

	gitDirectory, err := g.Output(ctx, Command{Args: []string{"rev-parse", "--git-dir"}, Dir: toplevel})
	if err != nil {
		return Preflight{}, err
	}
	if err := assertNoOperationInProgress(resolveAgainst(toplevel, gitDirectory)); err != nil {
		return Preflight{}, err
	}

	linked := g.detectLinkedWorktree(ctx, toplevel)
	checkoutBranch, _ := g.Try(ctx, Command{Args: []string{"symbolic-ref", "--short", "HEAD"}, Dir: toplevel})
	repositoryDefault := g.resolveRepositoryDefaultBranch(ctx, toplevel)

	// An empty explicit base means "start from the selected folder's branch".
	// Preferring origin/HEAD here is wrong for linked worktrees, which are
	// usually on a feature branch whose tip would be silently discarded.
	baseBranch := strings.TrimSpace(options.BaseBranch)
	if baseBranch == "" {
		baseBranch = checkoutBranch
	}
	if baseBranch == "" {
		baseBranch = repositoryDefault
	}
	if baseBranch == "" {
		return Preflight{}, newError(CodeAmbiguousBase,
			"HEAD is detached and the repository has no origin/HEAD, so a base branch cannot be resolved; check out a branch or set the base explicitly")
	}
	if !ValidBranchName(baseBranch) {
		return Preflight{}, newError(CodeInvalidBranch, "the base branch name is not a valid branch name")
	}

	defaultBranch := repositoryDefault
	if defaultBranch == "" {
		defaultBranch = checkoutBranch
	}
	if defaultBranch == "" {
		defaultBranch = baseBranch
	}

	baseSha, ok := g.Try(ctx, Command{
		Args: []string{"rev-parse", "--verify", baseBranch + "^{commit}"},
		Dir:  toplevel,
	})
	if !ok || !ValidObjectID(baseSha) {
		return Preflight{}, newError(CodeBaseNotFound, "the base branch does not exist in this repository")
	}

	remoteURL, _ := g.Try(ctx, Command{Args: []string{"remote", "get-url", "origin"}, Dir: toplevel})
	if remoteURL != "" {
		if err := ValidateRemoteURL(remoteURL); err != nil {
			// A hostile remote is not a reason to refuse preflight — it is a
			// reason to refuse publication. Drop it and let the publish gate
			// report the absence.
			remoteURL = ""
		}
	}

	return Preflight{
		Toplevel:         toplevel,
		BaseBranch:       baseBranch,
		BaseSha:          baseSha,
		RemoteURL:        remoteURL,
		DefaultBranch:    defaultBranch,
		CheckoutBranch:   checkoutBranch,
		IsLinkedWorktree: linked,
		IsGitRepository:  true,
	}, nil
}

func (g *Git) plainFolderPreflight(projectPath string) (Preflight, error) {
	info, err := os.Lstat(projectPath)
	if err != nil {
		return Preflight{}, newError(CodeInvalidPath, "the project folder does not exist").
			withDetail(err.Error()).withCause(err)
	}
	if !info.IsDir() {
		return Preflight{}, newError(CodeInvalidPath, "the project path is not a directory")
	}
	return Preflight{Toplevel: projectPath, IsGitRepository: false}, nil
}

// detectLinkedWorktree compares git-dir to git-common-dir. Parsing the `.git`
// file is not a durable signal because bare edge cases differ.
func (g *Git) detectLinkedWorktree(ctx context.Context, toplevel string) bool {
	gitDirRaw, ok := g.Try(ctx, Command{Args: []string{"rev-parse", "--git-dir"}, Dir: toplevel})
	if !ok {
		return false
	}
	commonRaw, ok := g.Try(ctx, Command{Args: []string{"rev-parse", "--git-common-dir"}, Dir: toplevel})
	if !ok {
		return false
	}
	gitDir, err := filepath.EvalSymlinks(resolveAgainst(toplevel, gitDirRaw))
	if err != nil {
		return false
	}
	commonDir, err := filepath.EvalSymlinks(resolveAgainst(toplevel, commonRaw))
	if err != nil {
		return false
	}
	return gitDir != commonDir
}

// resolveRepositoryDefaultBranch reads origin/HEAD. It stays a separate fact
// from the run base so a worktree on a feature branch can start from that
// feature tip while still reporting where the repository points by default.
func (g *Git) resolveRepositoryDefaultBranch(ctx context.Context, toplevel string) string {
	originHead, ok := g.Try(ctx, Command{
		Args: []string{"symbolic-ref", "--short", "refs/remotes/origin/HEAD"},
		Dir:  toplevel,
	})
	if !ok {
		return ""
	}
	return strings.TrimPrefix(originHead, "origin/")
}

func assertNoOperationInProgress(gitDirectory string) error {
	for _, operation := range operationMarkers {
		if _, err := os.Lstat(filepath.Join(gitDirectory, operation.marker)); err == nil {
			return newError(CodeRepositoryBusy, "cannot start a run while "+operation.message)
		}
	}
	return nil
}

func resolveAgainst(base, candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(base, candidate)
}
