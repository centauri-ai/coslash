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

const (
	ReasonInitialRefresh   Reason = "initial_refresh"
	ReasonBroaderHistory   Reason = "broader_history"
	ReasonHistoryTruncated Reason = "history_truncated"
	ReasonRefreshTimeout   Reason = "refresh_timeout"
	ReasonAuthentication   Reason = "authentication_failed"
	ReasonHostKey          Reason = "host_key_failed"
	ReasonConnectionFailed Reason = "connection_failed"
	ReasonSFTPUnavailable  Reason = "sftp_unavailable"
	ReasonPermissionDenied Reason = "permission_denied"
	ReasonNoSupportedData  Reason = "no_supported_data"
	ReasonPartialAgentData Reason = "partial_agent_data"
	ReasonInvalidData      Reason = "invalid_remote_data"
	ReasonLocalCacheFailed Reason = "local_cache_failed"
	ReasonDisabled         Reason = "disabled"
)

type Health struct {
	SourceID         string          `json:"sourceId"`
	Label            string          `json:"label"`
	State            State           `json:"state"`
	Complete         bool            `json:"complete"`
	Reason           *Reason         `json:"reason,omitempty"`
	LastSuccessAtMs  *int64          `json:"lastSuccessAtMs,omitempty"`
	CoverageSinceMs  *int64          `json:"coverageSinceMs,omitempty"`
	RoundTripMs      *int64          `json:"roundTripMs,omitempty"`
	Coverage         []AgentCoverage `json:"coverage,omitempty"`
	Error            string          `json:"error,omitempty"`
	DiagnosticStderr string          `json:"-"`
	Refreshing       bool            `json:"-"`
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
		return "SSH authentication failed"
	case ReasonHostKey:
		return "SSH host key verification failed"
	case ReasonConnectionFailed:
		return "connection failed"
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
	default:
		return "remote refresh failed"
	}
}
