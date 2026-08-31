package remote

import (
	"fmt"
	"strings"
	"time"

	obslog "github.com/centauri-ai/coslash/collector/internal/observe"
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
		parts = append(parts, fmt.Sprintf(
			"%s:cand=%d,sel=%d,trunc=%t,err=%t",
			item.Agent, item.CandidateFiles, item.SelectedFiles, item.Truncated, item.Error != "",
		))
	}
	return strings.Join(parts, ";")
}
