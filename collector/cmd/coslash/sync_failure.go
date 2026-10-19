package main

import (
	"fmt"
	"net/url"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

const hubFailureNoticeAfter = time.Minute

func hubFailureView(origin string, failure *syncv4.HubContactFailure, now time.Time) (*diagnostics.HubFailure, bool) {
	if failure == nil {
		return nil, false
	}
	host := "Hub"
	if parsed, err := url.Parse(origin); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	message := hubclient.HubFailureMessage(failure.Cause, failure.RawError)
	verb := "can't connect to"
	switch failure.Cause {
	case hubclient.FailureHubUnavailable:
		verb = "is temporarily unavailable at"
	case hubclient.FailureRejected:
		verb = "rejected a request at"
	}
	sentence := fmt.Sprintf("Hub: %s %s since %s (%d attempts) — %s", verb, host,
		time.UnixMilli(failure.SinceMs).In(time.Local).Format("15:04"), failure.Attempts, message)
	// The advertised idle cadence can be as long as 15 minutes. It controls
	// normal check-ins, but a missed contact should be visible after the
	// one-minute minimum retry interval.
	active := now.Sub(time.UnixMilli(failure.SinceMs)) > hubFailureNoticeAfter
	return &diagnostics.HubFailure{
		Host: host, Cause: failure.Cause, SinceMs: failure.SinceMs, Attempts: failure.Attempts,
		Sentence: sentence, RawError: failure.RawError,
	}, active
}
