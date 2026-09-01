package remote

import (
	"context"
	"errors"
)

// Helper collection needs reasons SFTP never produced. They stay distinct from
// transport failure so the UI can offer the right repair: install, upgrade, or
// simply retry. User-facing copy for these lands with the Machines UI work.
const (
	ReasonHelperMissing      Reason = "helper_missing"
	ReasonHelperBlocked      Reason = "helper_not_executable"
	ReasonHelperIncompatible Reason = "helper_incompatible"
	ReasonHelperFailed       Reason = "helper_failed"
	ReasonOutputLimit        Reason = "output_limit"
)

// classifyHelperError maps one collect or handshake failure to a stable reason.
// Helper outcomes are checked before the shared SSH text matching so a helper
// that ran and failed is never reported as a connection problem.
func classifyHelperError(err error) Reason {
	switch {
	case err == nil:
		return ReasonHelperFailed
	case errors.Is(err, ErrHelperMissing):
		return ReasonHelperMissing
	case errors.Is(err, ErrHelperBlocked):
		return ReasonHelperBlocked
	case errors.Is(err, ErrHelperIncompatible):
		return ReasonHelperIncompatible
	case errors.Is(err, ErrHelperPartial):
		return ReasonPartialAgentData
	case errors.Is(err, ErrHelperOutputLimit), errors.Is(err, ErrStderrLimit):
		return ReasonOutputLimit
	case errors.Is(err, ErrHelperRequestBounds):
		return ReasonOutputLimit
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return ReasonRefreshTimeout
	case errors.Is(err, ErrInvalidHelperPath), errors.Is(err, ErrInvalidHelperArgs),
		errors.Is(err, ErrInvalidAlias):
		return ReasonHelperFailed
	case errors.Is(err, ErrHelperFailed):
		return ReasonInvalidData
	default:
		return classifyError(err)
	}
}

func helperErrorCopy(reason Reason) string {
	switch reason {
	case ReasonHelperMissing:
		return "collection helper is not installed"
	case ReasonHelperBlocked:
		return "collection helper cannot be executed"
	case ReasonHelperIncompatible:
		return "collection helper needs an upgrade"
	case ReasonHelperFailed:
		return "collection helper failed"
	case ReasonOutputLimit:
		return "helper output exceeded safety limits"
	default:
		return genericErrorCopy(reason)
	}
}
