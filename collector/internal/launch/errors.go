package launch

import "errors"

var (
	ErrInvalidInput      = errors.New("launch: invalid input")
	ErrSessionNotFound   = errors.New("launch: session not found")
	ErrHandoffNotFound   = errors.New("launch: handoff not found")
	ErrHandoffExpired    = errors.New("launch: handoff expired")
	ErrHandoffUsed       = errors.New("launch: handoff already used")
	ErrAgentUnavailable  = errors.New("launch: agent unavailable")
	ErrWorkingDirectory  = errors.New("launch: working directory unavailable")
	ErrStartFailed       = errors.New("launch: starting agent")
	ErrTerminalOpen      = errors.New("launch: opening terminal")
	ErrUnsupportedOnHost = errors.New("launch: unsupported on this host")
)
