package main

import (
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/observe"
)

// issue records a product-area failure line for local debugging.
// The line is printed in the coslash server CLI and written to the daily log.
func issue(event string, fields ...any) {
	observe.Event("issue."+event, fields...)
}

func issueAPI(route, reason string, status int) {
	issue("api.error",
		"route", route,
		"reason", reason,
		"status", status,
		"detail", apiIssueDetail(route, reason),
	)
}

func apiIssueDetail(route, reason string) string {
	switch route + "|" + reason {
	case "sessions|bad_request", "sessions|bad_remote_since":
		return "invalid sessions query"
	case "sessions|list_failed":
		return "could not list sessions"
	case "diff|load_failed":
		return "could not load diff"
	case "diff|not_found":
		return "diff session not found"
	case "diff|file_not_found":
		return "diff file not found"
	case "share_preview|bad_request":
		return "invalid share preview request"
	case "share_preview|load_failed":
		return "could not load share preview"
	case "share_preview|not_found":
		return "share preview session not found"
	case "synthesis|load_failed":
		return "could not load synthesis"
	case "synthesis|not_found":
		return "synthesis session not found"
	case "launch|load_failed":
		return "could not load session for launch"
	case "launch|not_found":
		return "launch session not found"
	case "settings|too_large":
		return "settings payload too large"
	case "settings|invalid_json":
		return "invalid settings JSON"
	case "settings|invalid_synthesis":
		return "invalid synthesis settings"
	case "settings|save_failed":
		return "could not save settings"
	case "settings|remote_apply_failed":
		return "could not apply remote settings"
	case "remote_action|invalid_source":
		return "invalid source id"
	case "remote_action|remote_action_unsupported":
		return "remote action unsupported"
	default:
		return route + " " + reason
	}
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

func launchIssueDetail(reason string) string {
	switch reason {
	case "settings_invalid":
		return "launch blocked by invalid settings"
	case "no_cwd":
		return "session has no working directory"
	case "os_unsupported":
		return "terminal launch unsupported on this OS"
	case "terminal_missing":
		return "selected terminal is not available"
	case "terminal_unsupported":
		return "unsupported terminal selection"
	case "bad_mode":
		return "unknown launch mode"
	case "bad_handoff":
		return "invalid launch handoff"
	case "unknown_agent":
		return "unknown agent for launch"
	case "open_failed":
		return "could not open terminal"
	default:
		return "could not launch terminal"
	}
}
