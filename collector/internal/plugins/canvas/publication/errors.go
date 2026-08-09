package publication

import (
	"errors"
	"fmt"
	"os"
)

// Stable machine codes.
const (
	CodeRunNotReady       = "RUN_NOT_READY"
	CodePreflightFailed   = "PUBLISH_PREFLIGHT_FAILED"
	CodeNoGitHub          = "PUBLISH_NO_GITHUB"
	CodeUnsafeRemote      = "UNSAFE_REMOTE"
	CodeEmptyChange       = "PUBLISH_EMPTY_CHANGE"
	CodeControlPlanePaths = "PUBLISH_CONTROL_PLANE_PATHS"
	CodePublishFailed     = "PUBLISH_FAILED"
	CodeInvalidRequest    = "INVALID_REQUEST"
)

// ErrPublication marks every failure this package produces.
var ErrPublication = errors.New("publication: operation failed")

// Error carries a stable code and a client-safe message. `gh` and `git push`
// stderr can contain tokens and private remote paths, so it stays in detail.
type Error struct {
	Code    string
	Message string

	detail string
	cause  error
}

func newError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Detail returns withheld diagnostics for server-side logging only.
func (e *Error) Detail() string { return e.detail }

func (e *Error) Unwrap() error {
	if e.cause != nil {
		return e.cause
	}
	return ErrPublication
}

func (e *Error) Is(target error) bool { return target == ErrPublication }

func (e *Error) withDetail(detail string) *Error {
	e.detail = detail
	return e
}

func (e *Error) withCause(cause error) *Error {
	e.cause = cause
	return e
}

// lookupEnv is a seam so tests can bound the environment `gh` observes.
var lookupEnv = os.LookupEnv
