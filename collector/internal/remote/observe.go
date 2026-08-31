package remote

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// Testing-branch observability only. Emits local structured logs; no network export.
// Fields stay low-cardinality: reasons, timings, sizes — never paths, aliases, or stderr hosts.

func observe(event string, fields ...any) {
	parts := make([]string, 0, 1+len(fields)/2)
	parts = append(parts, "remote."+event)
	for i := 0; i+1 < len(fields); i += 2 {
		parts = append(parts, fmt.Sprintf("%v=%v", fields[i], fields[i+1]))
	}
	log.Print(strings.Join(parts, " "))
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
