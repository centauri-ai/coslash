package main

import (
	"fmt"
	"net/url"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

func hubFailureView(origin string, failure *syncv4.HubContactFailure, now time.Time, retryInterval time.Duration) (*diagnostics.HubFailure, bool) {
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
	active := now.Sub(time.UnixMilli(failure.SinceMs)) > retryInterval
	return &diagnostics.HubFailure{
		Host: host, Cause: failure.Cause, SinceMs: failure.SinceMs, Attempts: failure.Attempts,
		Sentence: sentence, RawError: failure.RawError,
	}, active
}
