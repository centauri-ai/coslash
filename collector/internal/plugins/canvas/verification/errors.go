package verification

import (
	"errors"
	"fmt"
	"os"
)

// Stable machine codes.
const (
	CodePolicyViolation = "POLICY_VIOLATION"
	CodeInvalidCheck    = "INVALID_CHECK"
	CodeRunNotReady     = "RUN_NOT_READY"
	CodeCheckFailed     = "CHECK_FAILED"
)

// ErrVerification marks every failure this package produces.
var ErrVerification = errors.New("verification: operation failed")

// Error carries a stable code and a client-safe message. Check output is the
// single most dangerous thing to echo — it routinely contains absolute private
// paths and file contents — so it stays in detail and in the stored log.
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
	return ErrVerification
}

func (e *Error) Is(target error) bool { return target == ErrVerification }

func (e *Error) withDetail(detail string) *Error {
	e.detail = detail
	return e
}

func (e *Error) withCause(cause error) *Error {
	e.cause = cause
	return e
}

// lookupEnv is a seam so tests can bound the environment a check observes
// without mutating the collector's own process environment.
var lookupEnv = os.LookupEnv
