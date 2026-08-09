package revision

import (
	"context"
	"os"
	"path/filepath"
)

// ExchangeDirectory is the control-plane directory inside a run root. The
// embedded `.fleetlog/run/**` protocol is preserved verbatim for parity with
// the legacy agents; renaming it is explicitly out of scope for this migration.
const ExchangeDirectory = ".fleetlog"

// RunRoot is where a run's agents work.
type RunRoot struct {
	// Path is the run root directory.
	Path string
	// Branch is the branch agents edit. For an in-place attach this is the
	// shared work branch, not a per-run branch.
	Branch string
	// BaseSha is the tip at clone or attach time — the change-capture baseline.
	BaseSha string
	// PublishBaseBranch is the pull-request target. It differs from Branch for
	// in-place runs, where agents accumulate on a dedicated long-lived branch.
	PublishBaseBranch string
	// PublishBaseSha is the tip of PublishBaseBranch at attach time, when
	// resolvable.
	PublishBaseSha string
	// InPlace reports that Path is the user's own project folder rather than a
	// disposable clone. Teardown must never delete an in-place root.
	InPlace bool
}

// CreateRunRootOptions selects where an isolated clone is created.
type CreateRunRootOptions struct {
	Preflight Preflight
	// Path must not exist. Its parent is created with private modes.
	Path string
	// Branch is the run branch created at Preflight.BaseSha.
	Branch string
}

// CreateRunRoot clones the user's repository into an isolated run root.
//
// The run root is a CLONE, not a linked worktree. A linked worktree shares
// config, hooks/, refs/, and objects/ with the user's repository, so an agent
// inside one can set core.hooksPath (code execution in the user's next commit),
// set credential.helper (exfiltrate the token), or force-remove the user's
// checkout. "We never write to the user's worktree" is the wrong invariant;
// owning a separate object store is the right one.
func (g *Git) CreateRunRoot(ctx context.Context, options CreateRunRootOptions) (RunRoot, error) {
	if !options.Preflight.IsGitRepository {
		return RunRoot{}, newError(CodeNotARepository, "a run root clone requires a git repository")
	}
	if !ValidBranchName(options.Branch) {
		return RunRoot{}, newError(CodeInvalidBranch, "the run branch name is not a valid branch name")
	}
	if options.Path == "" || !filepath.IsAbs(options.Path) {
		return RunRoot{}, newError(CodeInvalidPath, "the run root must be an absolute path")
	}
	if _, err := os.Lstat(options.Path); err == nil {
		return RunRoot{}, newError(CodeRunRootExists, "the run root already exists")
	}
	if !ValidObjectID(options.Preflight.BaseSha) {
		return RunRoot{}, newError(CodeBaseNotFound, "the preflight base commit is not a full object name")
	}
	if err := os.MkdirAll(filepath.Dir(options.Path), 0o700); err != nil {
		return RunRoot{}, newError(CodeInvalidPath, "the run root parent could not be created").
			withDetail(err.Error()).withCause(err)
	}

	// --no-hardlinks is not optional. Plain --local hardlinks the object files,
	// so an agent that rewrites one corrupts the user's repository through the
	// shared inode, defeating the entire reason for cloning.
	if _, err := g.Output(ctx, Command{Args: []string{
		"clone", "--local", "--no-hardlinks", "--no-checkout", "--",
		options.Preflight.Toplevel, options.Path,
	}}); err != nil {
		return RunRoot{}, err
	}

	// Point origin at the URL captured during preflight. The clone sets origin
	// to the local path it came from, which is neither where a pull request
	// should be pushed nor a path the run should be able to write through.
	if options.Preflight.RemoteURL != "" {
		if _, err := g.Output(ctx, Command{
			Args: []string{"-C", options.Path, "remote", "set-url", "origin", "--", options.Preflight.RemoteURL},
		}); err != nil {
			return RunRoot{}, err
		}
	} else if _, err := g.Output(ctx, Command{
		Args: []string{"-C", options.Path, "remote", "remove", "origin"},
	}); err != nil {
		return RunRoot{}, err
	}

	// Check out the recorded base SHA, not the branch name: the clone's branches
	// track whatever the source had at clone time, and the run was authorized
	// against one specific commit.
	if _, err := g.Output(ctx, Command{Args: []string{
		"-C", options.Path, "checkout", "--quiet", "-b", options.Branch, options.Preflight.BaseSha,
	}}); err != nil {
		return RunRoot{}, err
	}

	if err := g.WriteExchangeDirectory(ctx, options.Path); err != nil {
		return RunRoot{}, err
	}

	// A cloned run branches from the base the run was authorized against, so
	// that same branch is the pull-request target.
	return RunRoot{
		Path:              options.Path,
		Branch:            options.Branch,
		BaseSha:           options.Preflight.BaseSha,
		PublishBaseBranch: options.Preflight.BaseBranch,
		PublishBaseSha:    options.Preflight.BaseSha,
		InPlace:           false,
	}, nil
}

// AttachInPlaceRunRoot uses the project folder itself as the run root: check out
// the shared work branch and leave agents editing there so successive runs
// accumulate on that branch.
//
// Atlas needs this for git projects. Plain folders still use an isolated copy,
// and an in-place root is never deleted by teardown.
func (g *Git) AttachInPlaceRunRoot(ctx context.Context, ready Preflight) (RunRoot, error) {
	if !ready.IsGitRepository {
		return RunRoot{}, newError(CodeNotARepository, "in-place attach requires a git repository")
	}
	workBranch := ready.BaseBranch
	if !ValidBranchName(workBranch) {
		return RunRoot{}, newError(CodeInvalidBranch, "the work branch name is not a valid branch name")
	}

	gitDirectory, ok := g.Try(ctx, Command{Args: []string{"rev-parse", "--git-dir"}, Dir: ready.Toplevel})
	if !ok {
		return RunRoot{}, newError(CodeNotARepository, "the git directory could not be resolved")
	}
	if err := assertNoOperationInProgress(resolveAgainst(ready.Toplevel, gitDirectory)); err != nil {
		return RunRoot{}, err
	}

	checkoutBranch, _ := g.Try(ctx, Command{Args: []string{"symbolic-ref", "--short", "HEAD"}, Dir: ready.Toplevel})
	if checkoutBranch != workBranch {
		porcelain, _ := g.Try(ctx, Command{Args: []string{"status", "--porcelain"}, Dir: ready.Toplevel})
		if porcelain != "" {
			return RunRoot{}, newError(CodeDirtyWorktree,
				"the work branch cannot be checked out while the worktree has uncommitted changes")
		}
		if _, err := g.Output(ctx, Command{
			Args: []string{"checkout", "--quiet", workBranch}, Dir: ready.Toplevel,
		}); err != nil {
			return RunRoot{}, err
		}
	}

	baseSha, err := g.Output(ctx, Command{Args: []string{"rev-parse", "HEAD"}, Dir: ready.Toplevel})
	if err != nil {
		return RunRoot{}, err
	}
	if !ValidObjectID(baseSha) {
		return RunRoot{}, newError(CodeBaseNotFound, "the work branch tip is not a full object name")
	}

	publishBaseBranch := ready.DefaultBranch
	publishBaseSha, _ := g.Try(ctx, Command{
		Args: []string{"rev-parse", "--verify", publishBaseBranch + "^{commit}"},
		Dir:  ready.Toplevel,
	})
	if !ValidObjectID(publishBaseSha) {
		publishBaseSha = ""
	}

	if err := g.WriteExchangeDirectory(ctx, ready.Toplevel); err != nil {
		return RunRoot{}, err
	}

	return RunRoot{
		Path:              ready.Toplevel,
		Branch:            workBranch,
		BaseSha:           baseSha,
		PublishBaseBranch: publishBaseBranch,
		PublishBaseSha:    publishBaseSha,
		InPlace:           true,
	}, nil
}

// WriteExchangeDirectory creates the control-plane directory and proves it is
// ignored by git.
//
// The directory ignores itself with an ordinary `.gitignore` containing a
// single `*`, which matches the .gitignore too, so the whole tree disappears
// from `git status`.
//
// info/exclude was rejected as the alternative: for a linked worktree
// `git rev-parse --git-path info/exclude` resolves to the COMMON git dir, so
// the rule would silently leak into the user's checkout. A plain file needs no
// git state at all and is visible to anyone inspecting the run.
func (g *Git) WriteExchangeDirectory(ctx context.Context, runRoot string) error {
	if runRoot == "" || !filepath.IsAbs(runRoot) {
		return newError(CodeInvalidPath, "the run root must be an absolute path")
	}
	exchange := filepath.Join(runRoot, ExchangeDirectory)
	if err := os.MkdirAll(filepath.Join(exchange, "run"), 0o700); err != nil {
		return newError(CodeInvalidPath, "the exchange directory could not be created").
			withDetail(err.Error()).withCause(err)
	}
	if err := os.WriteFile(filepath.Join(exchange, ".gitignore"), []byte("*\n"), 0o600); err != nil {
		return newError(CodeInvalidPath, "the exchange ignore file could not be written").
			withDetail(err.Error()).withCause(err)
	}

	// Verify rather than assume: a .gitignore that does not actually ignore is a
	// silent path to committing the control plane into the user's pull request.
	if _, ok := g.Try(ctx, Command{
		Args: []string{"-C", runRoot, "check-ignore", "-q", ExchangeDirectory + "/run"},
	}); !ok {
		return newError(CodeExchangeNotIgnore,
			"the run exchange directory is not ignored by git; refusing to continue")
	}
	return nil
}

// RemoveRunRoot deletes a disposable run root.
//
// Two independent guards bound the deletion, because this is the only recursive
// removal in the package: the path must be absolute and sit directly beneath a
// directory named `roots`, and it must contain a `.git` entry. An in-place root
// fails the first guard, so the user's project folder can never be removed here
// even when a caller passes it by mistake.
func RemoveRunRoot(runRoot string) error {
	if runRoot == "" || !filepath.IsAbs(runRoot) {
		return newError(CodeInvalidPath, "the run root must be an absolute path")
	}
	cleaned := filepath.Clean(runRoot)
	if cleaned == string(filepath.Separator) {
		return newError(CodeInvalidPath, "the run root may not be the filesystem root")
	}
	if filepath.Base(filepath.Dir(cleaned)) != "roots" {
		return newError(CodeInvalidPath, "only run roots beneath a roots directory may be removed")
	}
	if _, err := os.Lstat(cleaned); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return newError(CodeInvalidPath, "the run root could not be inspected").
			withDetail(err.Error()).withCause(err)
	}
	if _, err := os.Lstat(filepath.Join(cleaned, ".git")); err != nil {
		return newError(CodeInvalidPath, "the run root does not look like a repository")
	}
	if err := os.RemoveAll(cleaned); err != nil {
		return newError(CodeInvalidPath, "the run root could not be removed").
			withDetail(err.Error()).withCause(err)
	}
	return nil
}
