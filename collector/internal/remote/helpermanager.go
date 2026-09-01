package remote

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
)

// Transport identifies the collection path that produced the current view.
// It is deliberately independent of the cache and wire protocol.
type Transport string

const (
	TransportSFTP   Transport = "sftp"
	TransportHelper Transport = "helper"
)

// HelperStatus contains only display-safe lifecycle facts. It never contains
// stderr, remote paths, release URLs, or an artifact digest.
type HelperStatus struct {
	State      LifecycleState `json:"state"`
	Version    string         `json:"version,omitempty"`
	Compatible bool           `json:"compatible"`
	Fallback   bool           `json:"fallback"`
	Reason     *Reason        `json:"reason,omitempty"`
}

// CollectionMetrics are bounded, content-free diagnostics for the last
// collection attempt.
type CollectionMetrics struct {
	RequestBytes  int   `json:"requestBytes,omitempty"`
	ResponseBytes int   `json:"responseBytes,omitempty"`
	Records       int   `json:"records,omitempty"`
	RoundTripMs   int64 `json:"roundTripMs,omitempty"`
}

// HelperReleaseProvider supplies an app-authenticated release document and
// exact artifact bytes. The frontend never supplies either value.
type HelperReleaseProvider interface {
	Load(context.Context) (SignedReleaseMetadata, []byte, error)
}

type lifecycleFactory func(alias string) (Lifecycle, error)

type helperTarget struct {
	path    string
	version string
	state   LifecycleState
}

func lifecycleStatus(result LifecycleResult) HelperStatus {
	status := HelperStatus{
		State: result.State, Version: result.Artifact.Version,
		Compatible: result.CanExecute, Fallback: result.Fallback,
	}
	if result.Reason != nil {
		status.Reason = reasonPtr(classifyLifecycleError(result.Reason))
	}
	return status
}

func helperRefreshWithOpen(
	ctx context.Context,
	alias string,
	since int64,
	now time.Time,
	baseline CachedSnapshotV2,
	target helperTarget,
	open OpenOptions,
) (refreshOutcome, error) {
	request, err := buildLocalRequest(
		fmt.Sprintf("helper-%d", now.UnixNano()), since, now.UnixMilli(), baseline.BaselineID, knownFamiliesFor(baseline),
	)
	if err != nil {
		return refreshOutcome{}, fmt.Errorf("build helper collection request: %w", err)
	}
	effectiveBaseline := baseline
	if request.BaselineMode == remoteprotocol.BaselineNone {
		// Omitted known data must not be treated as an implicit baseline.
		effectiveBaseline = CachedSnapshotV2{Version: cacheV2Version, CodexHeaders: baseline.CodexHeaders}
	}
	result, collectErr := HelperCollect(ctx, alias, target.path, request, toGeneration(effectiveBaseline), open)
	snapshot := fromGeneration(result.Proposal, result.Coverage, now.UnixMilli(), result.RoundTrip.Milliseconds(), nil)
	outcome := refreshOutcome{
		Snapshot: snapshot,
		Sessions: composeFromGeneration(result.Proposal, nullReadSource{}, nil, since),
		Stderr:   result.Stderr, RoundTrip: result.RoundTrip,
		Metrics: CollectionMetrics{RequestBytes: result.RequestBytes, ResponseBytes: result.ResponseBytes, Records: result.Records, RoundTripMs: result.RoundTrip.Milliseconds()},
	}
	if collectErr == nil {
		return outcome, nil
	}
	// A helper can have emitted valid family records before a bounded partial
	// result. Preserve those facts and show the exact limited reason; do not
	// hide a protocol/data failure behind a second SFTP pass.
	if result.Records > 1 {
		outcome.Failures = []error{collectErr}
		return outcome, nil
	}
	return outcome, collectErr
}

func defaultLifecycleFactory(trust TrustStore) lifecycleFactory {
	return func(alias string) (Lifecycle, error) {
		remote, err := NewSSHLifecycleRemote(alias, OpenOptions{})
		if err != nil {
			return Lifecycle{}, err
		}
		return Lifecycle{Remote: remote, Trust: trust}, nil
	}
}

func unavailableHelperStatus(err error) HelperStatus {
	return HelperStatus{
		State: LifecycleSFTP, Fallback: true,
		Reason: reasonPtr(classifyLifecycleError(err)),
	}
}

var errHelperReleaseUnavailable = errors.New("helper release is not configured")

// SetupHelper performs a consented setup or repair. It is the only Manager
// method that can create a helper target; refreshes only execute that stored,
// verified target.
func (manager *Manager) SetupHelper(ctx context.Context, consent Consent) Health {
	manager.mu.Lock()
	if manager.cfg == nil {
		manager.mu.Unlock()
		return Health{State: StateDisabled, Complete: true, Reason: reasonPtr(ReasonDisabled)}
	}
	config := *manager.cfg
	provider, factory := manager.releaseProvider, manager.lifecycleFactory
	manager.mu.Unlock()

	if provider == nil || factory == nil {
		manager.mu.Lock()
		status := unavailableHelperStatus(errHelperReleaseUnavailable)
		manager.helper = &status
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health
	}
	document, bytes, err := provider.Load(ctx)
	if err != nil {
		manager.mu.Lock()
		status := unavailableHelperStatus(err)
		manager.helper = &status
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health
	}
	lifecycle, err := factory(config.SSHAlias)
	if err != nil {
		manager.mu.Lock()
		status := unavailableHelperStatus(err)
		manager.helper = &status
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health
	}
	result := lifecycle.Setup(ctx, document, bytes, consent)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || manager.cfg.ID != config.ID || manager.cfg.SSHAlias != config.SSHAlias {
		return manager.healthLocked(manager.lastRequestedMs)
	}
	status := lifecycleStatus(result)
	manager.helper = &status
	if result.CanExecute {
		if err := manager.cache.StoreHelperVersion(config.ID, result.Artifact.Version); err != nil {
			status = unavailableHelperStatus(fmt.Errorf("%w: %v", ErrHelperInstallation, err))
			manager.helper = &status
			manager.helperTarget = nil
			return manager.healthLocked(manager.lastRequestedMs)
		}
		manager.helperTarget = &helperTarget{path: result.Path, version: result.Artifact.Version, state: result.State}
		manager.helperVersion = result.Artifact.Version
	} else {
		manager.helperTarget = nil
	}
	return manager.healthLocked(manager.lastRequestedMs)
}

// UninstallHelper removes exactly the currently verified artifact. It leaves
// the local host configuration untouched on error so the caller can retry or
// deliberately choose remove-only.
func (manager *Manager) UninstallHelper(ctx context.Context) error {
	manager.mu.Lock()
	if manager.cfg == nil || (manager.helperTarget == nil && manager.helperVersion == "") {
		manager.mu.Unlock()
		return nil
	}
	config := *manager.cfg
	version := manager.helperVersion
	if manager.helperTarget != nil {
		version = manager.helperTarget.version
	}
	provider, factory := manager.releaseProvider, manager.lifecycleFactory
	manager.mu.Unlock()
	if provider == nil || factory == nil {
		return errHelperReleaseUnavailable
	}
	document, _, err := provider.Load(ctx)
	if err != nil {
		return err
	}
	lifecycle, err := factory(config.SSHAlias)
	if err != nil {
		return err
	}
	if err := lifecycle.Uninstall(ctx, document, version); err != nil {
		return err
	}
	if err := manager.cache.RemoveHelperVersion(config.ID); err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg != nil && manager.cfg.ID == config.ID && manager.helperVersion == version {
		manager.helperTarget = nil
		manager.helperVersion = ""
		status := HelperStatus{State: LifecycleSFTP, Fallback: true, Reason: reasonPtr(ReasonHelperMissing)}
		manager.helper = &status
	}
	return nil
}

// TestHelper performs a small, non-persisting collection after setup. It uses
// the verified target already held by the manager, so a test can never execute
// a frontend- or response-supplied path. The normal requested-window refresh
// remains responsible for durable cache commits.
func (manager *Manager) TestHelper(ctx context.Context) Health {
	manager.mu.Lock()
	if manager.cfg == nil || !manager.cfg.Enabled || manager.helperTarget == nil {
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health
	}
	config := *manager.cfg
	target := *manager.helperTarget
	baseline := snapshotOrEmpty(manager.snapshot)
	refresh := manager.helperRefresh
	now := manager.now()
	manager.mu.Unlock()

	since := now.Add(-24 * time.Hour).UnixMilli()
	result, err := refresh(ctx, config.SSHAlias, since, now, baseline, target)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || manager.cfg.ID != config.ID || manager.cfg.SSHAlias != config.SSHAlias {
		return manager.healthLocked(manager.lastRequestedMs)
	}
	manager.transport = TransportHelper
	manager.metrics = metricsFor(result)
	if err != nil {
		manager.applyFailureLocked(classifyHelperError(err), result.Stderr)
		return manager.healthLocked(manager.lastRequestedMs)
	}
	if reason := limitedResultReason(result); reason != nil {
		manager.state = StateLimited
		manager.complete = false
		manager.reason = reasonPtr(*reason)
		manager.errorCopy = genericErrorCopy(*reason)
		return manager.healthLocked(manager.lastRequestedMs)
	}
	manager.state = StateOK
	manager.complete = true
	manager.reason = nil
	manager.errorCopy = ""
	manager.diagnostic = ""
	return manager.healthLocked(manager.lastRequestedMs)
}

// helperRefreshFunc is kept as a named boundary so manager tests can substitute
// a deterministic helper without accepting arbitrary remote paths.
type helperRefreshFunc func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error)
