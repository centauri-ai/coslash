package revision

import (
	"errors"
	"fmt"
)

// Stable machine codes. Callers switch on these; the human-facing message may be
// reworded without breaking a client.
const (
	CodeNotARepository    = "NOT_A_REPOSITORY"
	CodeBareRepository    = "BARE_REPOSITORY"
	CodeNoWorktree        = "NO_WORKTREE"
	CodeRepositoryBusy    = "REPOSITORY_BUSY"
	CodeAmbiguousBase     = "AMBIGUOUS_BASE"
	CodeBaseNotFound      = "BASE_NOT_FOUND"
	CodeInvalidBranch     = "INVALID_BRANCH"
	CodeRunRootExists     = "RUN_ROOT_EXISTS"
	CodeDirtyWorktree     = "DIRTY_WORKTREE"
	CodeExchangeNotIgnore = "EXCHANGE_NOT_IGNORED"
	CodePatchTooLarge     = "PATCH_TOO_LARGE"
	CodeGitFailed         = "GIT_FAILED"
	CodeInvalidPath       = "INVALID_PATH"
	CodeUnsafeRemote      = "UNSAFE_REMOTE"
)

// ErrGit marks every failure this package produces so callers can classify a
// wrapped error without depending on the concrete type.
var ErrGit = errors.New("revision: git operation failed")

// Error carries a stable code plus a message that is safe to hand to an API
// client. Raw command output stays in detail, which is only ever read by
// server-side logging — Error never renders it.
//
// This is deliberately stricter than the legacy TypeScript, which interpolated
// git's stderr straight into the client-visible message. Verification and diff
// output routinely contain absolute private paths and file contents.
type Error struct {
	Code    string
	Message string

	detail string
	cause  error
}

func newError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Detail returns the withheld diagnostic text for server-side logging. It may
// contain command output and private paths and must never reach an API client.
func (e *Error) Detail() string { return e.detail }

func (e *Error) Unwrap() error {
	if e.cause != nil {
		return e.cause
	}
	return ErrGit
}

// Is reports ErrGit for every error from this package so errors.Is(err, ErrGit)
// classifies failures that also wrap a concrete cause.
func (e *Error) Is(target error) bool { return target == ErrGit }

func (e *Error) withDetail(detail string) *Error {
	e.detail = detail
	return e
}

func (e *Error) withCause(cause error) *Error {
	e.cause = cause
	return e
}
