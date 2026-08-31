package main

import (
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/observe"
)

// issue records a product-area failure line for local debugging.
func issue(event string, fields ...any) {
	observe.Event("issue."+event, fields...)
}

func issueAPI(route, reason string, status int) {
	issue("api.error", "route", route, "reason", reason, "status", status)
}

func launchIssueReason(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "no working directory"):
		return "no_cwd"
	case strings.Contains(message, "not supported on"):
		return "os_unsupported"
	case strings.Contains(message, "not installed") || strings.Contains(message, "not available"):
		return "terminal_missing"
	case strings.Contains(message, "unsupported terminal"):
		return "terminal_unsupported"
	case strings.Contains(message, "unknown mode"):
		return "bad_mode"
	case strings.Contains(message, "handoff"):
		return "bad_handoff"
	case strings.Contains(message, "unknown agent"):
		return "unknown_agent"
	case strings.Contains(message, "open "):
		return "open_failed"
	default:
		return "other"
	}
}
