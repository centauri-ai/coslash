package remote

import (
	"context"
	"fmt"
	"time"

	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

type classifiedFailure struct {
	State            State
	Reason           Reason
	ReachedCollector bool
}

func classifyRunFailure(result RunResult, overflow error, transportErr error, forProbe bool) classifiedFailure {
	if overflow != nil {
		return classifiedFailure{State: StateError, Reason: ReasonCollectorFailed}
	}
	if result.ExitCode == -1 {
		return classifiedFailure{State: StateError, Reason: ReasonRefreshTimeout}
	}
	if result.ExitCode == 127 {
		return classifiedFailure{State: StateSetupRequired, Reason: ReasonCollectorMissing}
	}
	if result.ExitCode == 255 {
		return classifiedFailure{State: StateError, Reason: ReasonConnectionFailed}
	}
	if result.ExitCode != 0 {
		return classifiedFailure{State: StateError, Reason: ReasonCollectorFailed}
	}
	if transportErr != nil {
		if forProbe {
			return classifiedFailure{State: StateUpgradeRequired, Reason: ReasonCollectorOutdated, ReachedCollector: true}
		}
		return classifiedFailure{State: StateError, Reason: ReasonInvalidRemoteTransport}
	}
	return classifiedFailure{State: StateError, Reason: ReasonCollectorFailed}
}

func extractAndDecodeView(stdout []byte) (remoteviewv1.View, []byte, error) {
	payload, prefix, err := remoteviewv1.ExtractFrame(stdout)
	if err != nil {
		return remoteviewv1.View{}, prefix, err
	}
	if len(prefix) > MaxPrefixNoise {
		return remoteviewv1.View{}, prefix, fmt.Errorf("%w: startup noise exceeded limit", remoteviewv1.ErrInvalidFrame)
	}
	view, err := remoteviewv1.Decode(payload)
	return view, prefix, err
}

func extractAndDecodeProbe(stdout []byte) (remoteviewv1.Probe, []byte, error) {
	payload, prefix, err := remoteviewv1.ExtractFrame(stdout)
	if err != nil {
		return remoteviewv1.Probe{}, prefix, err
	}
	if len(prefix) > MaxPrefixNoise {
		return remoteviewv1.Probe{}, prefix, fmt.Errorf("%w: startup noise exceeded limit", remoteviewv1.ErrInvalidFrame)
	}
	probe, err := remoteviewv1.DecodeProbe(payload)
	return probe, prefix, err
}

func retryBackoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	backoff := InitialRetryBackoff
	for i := 1; i < failures; i++ {
		if backoff >= MaxRetryBackoff/2 {
			return MaxRetryBackoff
		}
		backoff *= 2
	}
	if backoff > MaxRetryBackoff {
		return MaxRetryBackoff
	}
	return backoff
}

func probeSupportsView(probe remoteviewv1.Probe) bool {
	for _, capability := range probe.Capabilities {
		if capability == remoteviewv1.CapabilityRemoteView {
			return true
		}
	}
	return false
}

func midContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = SnapshotDeadline
	}
	return context.WithTimeout(parent, timeout)
}
