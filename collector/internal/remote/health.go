package remote

import (
	"strings"
	"unicode/utf8"
)

type State string

const (
	StateOK         State = "ok"
	StateConnecting State = "connecting"
	StateLimited    State = "limited"
	StateStale      State = "stale"
	StateError      State = "error"
	StateDisabled   State = "disabled"
)

type Reason string

type ActionRequired string

const (
	ActionAuthenticate  ActionRequired = "authenticate"
	ActionVerifyHostKey ActionRequired = "verify_host_key"
)

const (
	ReasonInitialRefresh      Reason = "initial_refresh"
	ReasonBroaderHistory      Reason = "broader_history"
	ReasonHistoryTruncated    Reason = "history_truncated"
	ReasonRefreshTimeout      Reason = "refresh_timeout"
	ReasonAuthentication      Reason = "authentication_failed"
	ReasonHostKeyConfirmation Reason = "host_key_confirmation_required"
	ReasonHostKeyChanged      Reason = "host_key_changed"
	ReasonConnectionFailed    Reason = "connection_failed"
	ReasonSFTPUnavailable     Reason = "sftp_unavailable"
	ReasonPermissionDenied    Reason = "permission_denied"
	ReasonNoSupportedData     Reason = "no_supported_data"
	ReasonPartialAgentData    Reason = "partial_agent_data"
	ReasonInvalidData         Reason = "invalid_remote_data"
	ReasonLocalCacheFailed    Reason = "local_cache_failed"
	ReasonDisabled            Reason = "disabled"
)

type Health struct {
	SourceID                    string            `json:"sourceId"`
	Label                       string            `json:"label"`
	State                       State             `json:"state"`
	Complete                    bool              `json:"complete"`
	Reason                      *Reason           `json:"reason,omitempty"`
	ActionRequired              ActionRequired    `json:"actionRequired,omitempty"`
	AuthState                   AuthState         `json:"authState"`
	LastSuccessAtMs             *int64            `json:"lastSuccessAtMs,omitempty"`
	LastCheckedAtMs             *int64            `json:"lastCheckedAtMs,omitempty"`
	SessionCount                int               `json:"sessionCount"`
	CoverageSinceMs             *int64            `json:"coverageSinceMs,omitempty"`
	RoundTripMs                 *int64            `json:"roundTripMs,omitempty"`
	Coverage                    []AgentCoverage   `json:"coverage,omitempty"`
	Error                       string            `json:"error,omitempty"`
	Refreshing                  bool              `json:"-"`
	Transport                   Transport         `json:"transport"`
	Helper                      *HelperStatus     `json:"helper,omitempty"`
	Metrics                     CollectionMetrics `json:"metrics"`
	HelperInstallationAvailable bool              `json:"helperInstallationAvailable"`
	HelperProbeState            string            `json:"helperProbeState"`
	// HelperOwnershipRecorded is deliberately separate from Helper.Version:
	// a persisted ownership record survives a failed read-only inspection and
	// must still prevent a silent alias replacement.
	HelperOwnershipRecorded bool `json:"helperOwnershipRecorded"`
	HelperOwnershipCorrupt  bool `json:"helperOwnershipCorrupt"`
}

// WithAuthenticationStatus keeps connection remediation distinct from whether
// cached machine data is fresh. It is deliberately safe to call for every
// response, including an unconfigured machine.
func WithAuthenticationStatus(health Health) Health {
	health.AuthState = AuthNotRequired
	if health.Reason != nil {
		switch *health.Reason {
		case ReasonAuthentication, ReasonHostKeyConfirmation:
			health.ActionRequired = ActionAuthenticate
			health.AuthState = AuthRequired
		case ReasonHostKeyChanged:
			health.ActionRequired = ActionVerifyHostKey
		}
	}
	if health.ActionRequired != ActionVerifyHostKey && health.Label != "" && AuthAttemptActive(health.Label) {
		health.ActionRequired = ActionAuthenticate
		health.AuthState = AuthWaiting
	}
	return health
}

func reasonPtr(reason Reason) *Reason { return &reason }

func int64Ptr(value int64) *int64 { return &value }

func boundCopy(text string, max int) string {
	if max <= 0 || text == "" {
		return ""
	}
	if len(text) <= max {
		return strings.ToValidUTF8(text, "�")
	}
	truncated := text[:max]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

func redactDiagnostic(text string) string {
	text = strings.ToValidUTF8(text, "�")
	text = strings.ReplaceAll(text, "\x00", "")
	fields := strings.Fields(text)
	for index, field := range fields {
		if strings.HasPrefix(field, "/") || strings.Contains(field, "@") {
			fields[index] = "[redacted]"
		}
	}
	return boundCopy(strings.Join(fields, " "), MaxDiagnosticBytes)
}

func genericErrorCopy(reason Reason) string {
	switch reason {
	case ReasonAuthentication:
		return "SSH authentication required"
	case ReasonHostKeyConfirmation:
		return "SSH host key confirmation required"
	case ReasonHostKeyChanged:
		return "SSH host key changed — verify the host identity before reconnecting"
	case ReasonConnectionFailed:
		return "SSH is not reachable yet — check the alias, then Retry"
	case ReasonSFTPUnavailable:
		return "SFTP subsystem unavailable"
	case ReasonPermissionDenied:
		return "agent data is not readable"
	case ReasonNoSupportedData:
		return "no Claude or Codex data found"
	case ReasonPartialAgentData:
		return "some agent data is unavailable"
	case ReasonInvalidData:
		return "remote agent data could not be parsed"
	case ReasonLocalCacheFailed:
		return "remote snapshot could not be cached"
	case ReasonRefreshTimeout:
		return "refresh timed out"
	case ReasonHistoryTruncated:
		return "history truncated by safety limits"
	case ReasonHelperMissing, ReasonHelperBlocked, ReasonHelperIncompatible,
		ReasonHelperFailed, ReasonOutputLimit, ReasonHelperUnsupported,
		ReasonHelperVerification, ReasonHelperInstallation, ReasonHelperRevoked,
		ReasonHelperUpgrade, ReasonHelperRollback:
		return helperErrorCopy(reason)
	default:
		return "remote refresh failed"
	}
}
