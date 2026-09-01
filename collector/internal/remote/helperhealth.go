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
	ReasonHelperUnsupported  Reason = "helper_platform_unsupported"
	ReasonHelperVerification Reason = "helper_verification_failed"
	ReasonHelperInstallation Reason = "helper_installation_failed"
	ReasonHelperRevoked      Reason = "helper_revoked"
	ReasonHelperUpgrade      Reason = "helper_upgrade_required"
	ReasonHelperRollback     Reason = "helper_rolled_back"
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
		return ReasonHelperFailed
	default:
		return classifyError(err)
	}
}

// classifyLifecycleError keeps install/setup outcomes distinct from collection
// failures so callers can accurately offer SFTP, consent, or repair actions.
func classifyLifecycleError(err error) Reason {
	switch {
	case errors.Is(err, errHelperReleaseUnavailable):
		return ReasonHelperMissing
	case errors.Is(err, ErrUnsupportedHelperPlatform):
		return ReasonHelperUnsupported
	case errors.Is(err, ErrHelperMetadata), errors.Is(err, ErrHelperArtifact),
		errors.Is(err, ErrHelperVerification):
		return ReasonHelperVerification
	case errors.Is(err, ErrHelperNoExec):
		return ReasonHelperBlocked
	case errors.Is(err, ErrHelperRevoked):
		return ReasonHelperRevoked
	case errors.Is(err, ErrHelperConsentRequired), errors.Is(err, ErrHelperUpgradeRequired),
		errors.Is(err, ErrHelperIncompatible):
		return ReasonHelperUpgrade
	case errors.Is(err, ErrHelperRollback):
		return ReasonHelperRollback
	case errors.Is(err, ErrHelperInstallation):
		return ReasonHelperInstallation
	default:
		return classifyHelperError(err)
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
	case ReasonHelperUnsupported:
		return "remote platform cannot run the collection helper"
	case ReasonHelperVerification:
		return "collection helper could not be verified"
	case ReasonHelperInstallation:
		return "collection helper could not be installed"
	case ReasonHelperRevoked:
		return "collection helper was revoked and will not run"
	case ReasonHelperUpgrade:
		return "collection helper needs an approved upgrade"
	case ReasonHelperRollback:
		return "collection helper upgrade was rolled back"
	default:
		return genericErrorCopy(reason)
	}
}
