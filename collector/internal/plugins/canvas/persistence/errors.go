package persistence

import (
	"errors"
	"fmt"
)

// Stable API error codes. These are part of the client contract and must not be
// renamed once released; new failure modes get new codes.
const (
	CodeInvalidSession     = "INVALID_SESSION"
	CodeInvalidState       = "INVALID_STATE"
	CodeSchemaUnsupported  = "SCHEMA_UNSUPPORTED"
	CodeRevisionConflict   = "REVISION_CONFLICT"
	CodeStateTooLarge      = "STATE_TOO_LARGE"
	CodeQuotaExceeded      = "QUOTA_EXCEEDED"
	CodeStateCorrupt       = "STATE_CORRUPT"
	CodePersistenceFailed  = "PERSISTENCE_FAILED"
	CodeMalformedRequest   = "MALFORMED_REQUEST"
	CodeMethodNotAllowed   = "METHOD_NOT_ALLOWED"
	CodeRequestTooLarge    = "REQUEST_TOO_LARGE"
	CodeUnsupportedContent = "UNSUPPORTED_CONTENT_TYPE"
)

var (
	// ErrInvalidSession reports an identity that is empty, oversized, or
	// contains characters that may not appear in a stored identity.
	ErrInvalidSession = errors.New("persistence: invalid session identity")
	// ErrInvalidState reports state that is not a self-contained JSON value.
	ErrInvalidState = errors.New("persistence: invalid workspace state")
	// ErrSchemaUnsupported reports a document schema this build cannot serve.
	ErrSchemaUnsupported = errors.New("persistence: unsupported schema version")
	// ErrConflict reports a write against a revision that is no longer current.
	ErrConflict = errors.New("persistence: revision conflict")
	// ErrStateTooLarge reports state beyond the configured per-document bound.
	ErrStateTooLarge = errors.New("persistence: workspace state exceeds the size limit")
	// ErrQuotaExceeded reports that a new document would exceed the store bound.
	ErrQuotaExceeded = errors.New("persistence: workspace document quota exceeded")
	// ErrCorrupt reports a stored document that cannot be decoded.
	ErrCorrupt = errors.New("persistence: stored document is corrupt")
)

// ConflictError carries the current revision so a client can rebase without an
// extra read. It is the only error that exposes server state to the client.
type ConflictError struct {
	Expected uint64
	Actual   uint64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%v: expected revision %d, current revision %d", ErrConflict, e.Expected, e.Actual)
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

// CorruptionError reports an undecodable document. Reason is a short, safe
// description; it never carries file contents or private absolute paths.
type CorruptionError struct {
	Reason string
}

func (e *CorruptionError) Error() string {
	return fmt.Sprintf("%v: %s", ErrCorrupt, e.Reason)
}

func (e *CorruptionError) Unwrap() error { return ErrCorrupt }

// Code maps an error to its stable client-facing code. Unrecognized errors
// collapse to CodePersistenceFailed so internal details never reach a client.
func Code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalidSession):
		return CodeInvalidSession
	case errors.Is(err, ErrInvalidState):
		return CodeInvalidState
	case errors.Is(err, ErrSchemaUnsupported):
		return CodeSchemaUnsupported
	case errors.Is(err, ErrConflict):
		return CodeRevisionConflict
	case errors.Is(err, ErrStateTooLarge):
		return CodeStateTooLarge
	case errors.Is(err, ErrQuotaExceeded):
		return CodeQuotaExceeded
	case errors.Is(err, ErrCorrupt):
		return CodeStateCorrupt
	default:
		return CodePersistenceFailed
	}
}

// Message maps an error to a safe client-facing message. Messages describe the
// failure and the recovery action without leaking paths or internal errors.
func Message(err error) string {
	switch Code(err) {
	case CodeInvalidSession:
		return "The session identity is not valid."
	case CodeInvalidState:
		return "The workspace state is not a valid JSON object."
	case CodeSchemaUnsupported:
		return "The workspace schema version is not supported by this build."
	case CodeRevisionConflict:
		return "The workspace changed in another window; reload before saving again."
	case CodeStateTooLarge:
		return "The workspace state is too large to save."
	case CodeQuotaExceeded:
		return "The workspace store is full; remove unused workspaces before saving new ones."
	case CodeStateCorrupt:
		return "The stored workspace could not be read and must be rewritten from revision 0."
	default:
		return "The workspace could not be saved."
	}
}
