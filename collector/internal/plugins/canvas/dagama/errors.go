package dagama

import (
	"errors"
	"fmt"
)

// Stable machine codes. These reach API clients, so they are part of the
// contract and must not be reworded.
const (
	CodeInvalidRunID     = "INVALID_RUN_ID"
	CodeInvalidBoardID   = "INVALID_BOARD_ID"
	CodeInvalidProjectID = "INVALID_PROJECT_ID"
	CodeProjectNotOpen   = "PROJECT_NOT_OPEN"
	CodePolicyViolation  = "POLICY_VIOLATION"
	CodeInvalidState     = "INVALID_STATE"
	CodeRevisionConflict = "REVISION_CONFLICT"
	CodeNotFound         = "NOT_FOUND"
	CodeCorruptDocument  = "CORRUPT_DOCUMENT"
	CodeUnsafePath       = "UNSAFE_STORAGE_PATH"
	CodeLogFull          = "RUN_LOG_FULL"
	CodeSchemaVersion    = "UNSUPPORTED_SCHEMA_VERSION"
	CodeStorageFailed    = "STORAGE_FAILED"
)

// ErrDaGama marks every failure this package produces.
var ErrDaGama = errors.New("dagama: operation failed")

// Error carries a stable code, a client-safe message, and an optional field
// path for form-level reporting. Detail holds withheld diagnostics — board
// contents and private paths never reach Message.
type Error struct {
	Code    string
	Message string
	// Field is the dotted path of the offending value, for example
	// "components.build.seat.model". Empty when the failure is not field-scoped.
	Field string
	// ActualRevision is set on a revision conflict so a client can rebase
	// without a second round trip.
	ActualRevision *uint64

	detail string
	cause  error
}

func newError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func policyError(field, message string) *Error {
	return &Error{Code: CodePolicyViolation, Message: message, Field: field}
}

func (e *Error) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s at %s: %s", e.Code, e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Detail returns withheld diagnostics for server-side logging only.
func (e *Error) Detail() string { return e.detail }

func (e *Error) Unwrap() error {
	if e.cause != nil {
		return e.cause
	}
	return ErrDaGama
}

func (e *Error) Is(target error) bool { return target == ErrDaGama }

func (e *Error) withDetail(detail string) *Error {
	e.detail = detail
	return e
}

func (e *Error) withCause(cause error) *Error {
	e.cause = cause
	return e
}

func (e *Error) withActualRevision(revision uint64) *Error {
	e.ActualRevision = &revision
	return e
}
