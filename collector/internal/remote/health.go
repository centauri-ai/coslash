package remote

import (
	"strings"
	"unicode/utf8"
)

// State is the machine health state closed enum.
type State string

const (
	StateOK              State = "ok"
	StateConnecting      State = "connecting"
	StateLimited         State = "limited"
	StateSetupRequired   State = "setup_required"
	StateUpgradeRequired State = "upgrade_required"
	StateStale           State = "stale"
	StateError           State = "error"
	StateDisabled        State = "disabled"
)

// Reason is the optional machine health reason closed enum.
type Reason string

const (
	ReasonInitialRefresh         Reason = "initial_refresh"
	ReasonBroaderHistory         Reason = "broader_history"
	ReasonHistoryTruncated       Reason = "history_truncated"
	ReasonRefreshTimeout         Reason = "refresh_timeout"
	ReasonCollectorMissing       Reason = "collector_missing"
	ReasonCollectorOutdated      Reason = "collector_outdated"
	ReasonConnectionFailed       Reason = "connection_failed"
	ReasonCollectorFailed        Reason = "collector_failed"
	ReasonInvalidRemoteTransport Reason = "invalid_remote_transport"
	ReasonDisabled               Reason = "disabled"
)

// Health is the single remote machine health model for list and diagnostics.
type Health struct {
	SourceID         string   `json:"sourceId"`
	Label            string   `json:"label"`
	State            State    `json:"state"`
	Complete         bool     `json:"complete"`
	Reason           *Reason  `json:"reason,omitempty"`
	CollectorVersion string   `json:"collectorVersion,omitempty"`
	SchemaVersion    string   `json:"schemaVersion,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	LaunchableAgents []string `json:"launchableAgents,omitempty"`
	HostOS           string   `json:"hostOs,omitempty"`
	HostArch         string   `json:"hostArch,omitempty"`
	LastSuccessAtMs  *int64   `json:"lastSuccessAtMs,omitempty"`
	CoverageSinceMs  *int64   `json:"coverageSinceMs,omitempty"`
	ClockOffsetMs    *int64   `json:"clockOffsetMs,omitempty"`
	RoundTripMs      *int64   `json:"roundTripMs,omitempty"`
	Error            string   `json:"error,omitempty"`
	DiagnosticStderr string   `json:"-"`
	Refreshing       bool     `json:"-"`
}

func reasonPtr(r Reason) *Reason { return &r }

func int64Ptr(v int64) *int64 { return &v }

func boundCopy(text string, max int) string {
	if max <= 0 || text == "" {
		return ""
	}
	if len(text) <= max {
		if utf8.ValidString(text) {
			return text
		}
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
	// Avoid retaining home-directory paths and user@host forms in diagnostics.
	fields := strings.Fields(text)
	for i, field := range fields {
		if strings.HasPrefix(field, "/") || strings.Contains(field, "@") {
			fields[i] = "[redacted]"
		}
	}
	return boundCopy(strings.Join(fields, " "), MaxDiagnosticBytes)
}

func genericErrorCopy(reason Reason) string {
	switch reason {
	case ReasonCollectorMissing:
		return "collector missing"
	case ReasonCollectorOutdated:
		return "collector upgrade required"
	case ReasonConnectionFailed:
		return "connection failed"
	case ReasonCollectorFailed:
		return "collector failed"
	case ReasonInvalidRemoteTransport:
		return "invalid remote transport"
	case ReasonRefreshTimeout:
		return "refresh timed out"
	case ReasonHistoryTruncated:
		return "history truncated"
	default:
		return "remote refresh failed"
	}
}
