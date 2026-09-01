package remote

import (
	"fmt"
	"strings"
	"time"

	obslog "github.com/centauri-ai/coslash/collector/internal/observe"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

// ObserveEnabled reports whether remote/product debug logging is active.
func ObserveEnabled() bool {
	return obslog.Enabled()
}

// LogDir is the shared local log directory under COSLASH_HOME.
func LogDir() string {
	return obslog.LogDir()
}

// Observe emits a remote.* step line through the shared local issue recorder.
func Observe(event string, fields ...any) {
	observe(event, fields...)
}

func observe(event string, fields ...any) {
	obslog.Event("remote."+event, fields...)
}

func ms(d time.Duration) int64 {
	return d.Milliseconds()
}

func reasonOrEmpty(reason *Reason) string {
	if reason == nil {
		return ""
	}
	return string(*reason)
}

func coverageSummary(coverage []AgentCoverage) string {
	if len(coverage) == 0 {
		return ""
	}
	parts := make([]string, 0, len(coverage))
	for _, item := range coverage {
		part := fmt.Sprintf(
			"%s:cand=%d,sel=%d,trunc=%t",
			item.Agent, item.CandidateFiles, item.SelectedFiles, item.Truncated,
		)
		if item.ErrorReason != "" {
			part += ",reason=" + item.ErrorReason
		} else if item.Error != "" {
			part += ",reason=error"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ";")
}

func agentSessionCounts(sessions []*session.Session) string {
	if len(sessions) == 0 {
		return ""
	}
	counts := map[string]int{}
	order := make([]string, 0, 2)
	for _, item := range sessions {
		if item == nil {
			continue
		}
		if _, seen := counts[item.Agent]; !seen {
			order = append(order, item.Agent)
		}
		counts[item.Agent]++
	}
	parts := make([]string, 0, len(order))
	for _, agent := range order {
		parts = append(parts, fmt.Sprintf("%s=%d", agent, counts[agent]))
	}
	return strings.Join(parts, ",")
}

func observeCollect(
	agent string,
	coverage AgentCoverage,
	sessionCount int,
	duration time.Duration,
	bytes int64,
	entries int64,
) {
	outcome := "ok"
	if coverage.ErrorReason != "" || coverage.Error != "" {
		outcome = "error"
	}
	observe("collect",
		"agent", agent,
		"outcome", outcome,
		"reason", coverage.ErrorReason,
		"cand", coverage.CandidateFiles,
		"sel", coverage.SelectedFiles,
		"trunc", coverage.Truncated,
		"sessions", sessionCount,
		"bytes", bytes,
		"entries", entries,
		"duration_ms", ms(duration),
	)
}
