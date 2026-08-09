package runfs

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidPath = errors.New("runfs: invalid scoped path")
	ErrSymlink     = errors.New("runfs: symbolic links are not allowed")
	ErrNotRegular  = errors.New("runfs: path is not a regular file or directory")
	ErrTooLarge    = errors.New("runfs: data exceeds configured limit")
	ErrCorruptLog  = errors.New("runfs: event log is corrupt")
	ErrLogFull     = errors.New("runfs: event log is full")
)

// CorruptionError identifies the first invalid durable line in an event log.
// It intentionally omits the private absolute path from its message.
type CorruptionError struct {
	Line   uint64
	Reason string
}

func (e *CorruptionError) Error() string {
	return fmt.Sprintf("%v at line %d: %s", ErrCorruptLog, e.Line, e.Reason)
}

func (e *CorruptionError) Unwrap() error { return ErrCorruptLog }
