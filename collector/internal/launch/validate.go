package launch

import (
	"fmt"
	"regexp"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

var (
	handoffIDPattern = regexp.MustCompile(`^h_[0-9a-f]{32}$`)
	sshAliasPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func ValidateRemoteAgent(agent string) error {
	switch agent {
	case vendors.AgentClaude, vendors.AgentCodex:
		return nil
	default:
		return fmt.Errorf("%w: unsupported agent %q", ErrInvalidInput, agent)
	}
}

func ValidateUUIDSessionID(sessionID string) error {
	if !uuidSessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("%w: %q is not a session id", ErrInvalidInput, sessionID)
	}
	return nil
}

func ValidateHandoffID(id string) error {
	if !handoffIDPattern.MatchString(id) {
		return fmt.Errorf("%w: %q is not a handoff id", ErrInvalidInput, id)
	}
	return nil
}

func ValidateSSHAlias(alias string) error {
	if !sshAliasPattern.MatchString(alias) {
		return fmt.Errorf("%w: %q is not an ssh alias", ErrInvalidInput, alias)
	}
	return nil
}

func ValidateRemoteMode(mode, handoffID string) error {
	switch mode {
	case ResumeSession:
		if handoffID != "" {
			return fmt.Errorf("%w: resume does not accept --handoff", ErrInvalidInput)
		}
		return nil
	case NewSession:
		if handoffID == "" {
			return fmt.Errorf("%w: new mode requires --handoff", ErrInvalidInput)
		}
		return ValidateHandoffID(handoffID)
	default:
		return fmt.Errorf("%w: unknown mode %q", ErrInvalidInput, mode)
	}
}
