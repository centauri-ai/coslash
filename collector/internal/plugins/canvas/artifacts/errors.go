package artifacts

import (
	"errors"
	"fmt"
)

// Stable machine codes. missing_output and invalid_output are the reasons the
// legacy run cards already surface, so they are preserved verbatim; the rest
// follow the suite's uppercase convention.
const (
	CodeMissingOutput  = "missing_output"
	CodeInvalidOutput  = "invalid_output"
	CodeRunNotReady    = "RUN_NOT_READY"
	CodeUnknownBlob    = "UNKNOWN_ARTIFACT"
	CodeNotFound       = "NOT_FOUND"
	CodeUnsafePath     = "UNSAFE_STORAGE_PATH"
	CodeBlobConflict   = "ARTIFACT_BLOB_CONFLICT"
	CodeManifestFailed = "ARTIFACT_MANIFEST_FAILED"
)

// ErrArtifact marks every failure this package produces.
var ErrArtifact = errors.New("artifacts: operation failed")

// Error carries a stable code and a client-safe message. Detail holds withheld
// diagnostics — file contents and private paths never reach Message.
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
	return ErrArtifact
}

func (e *Error) Is(target error) bool { return target == ErrArtifact }

func (e *Error) withDetail(detail string) *Error {
	e.detail = detail
	return e
}

func (e *Error) withCause(cause error) *Error {
	e.cause = cause
	return e
}
