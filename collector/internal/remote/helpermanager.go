package remote

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

// Transport identifies the collection path that produced the current view.
// It is deliberately independent of the cache and wire protocol.
type Transport string

const (
	TransportSFTP   Transport = "sftp"
	TransportHelper Transport = "helper"
)

// useSFTPFallbackAfterDiscovery is the complete pre-execution policy. A
// helper is selected only after it is verified and capability-compatible;
// every other discovery state retains usable, visibly labelled SFTP. Runtime
// helper protocol/data/output failures deliberately do not call this policy:
// they preserve cache and report the helper failure without masking it.
func useSFTPFallbackAfterDiscovery(result LifecycleResult) bool {
	return !result.CanExecute
}

// HelperStatus contains only display-safe lifecycle facts. It never contains
// stderr, remote paths, release URLs, or an artifact digest.
type HelperStatus struct {
	State      LifecycleState `json:"state"`
	Version    string         `json:"version,omitempty"`
	Compatible bool           `json:"compatible"`
	Fallback   bool           `json:"fallback"`
	// Reused is true only when the currently selected helper was freshly
	// re-verified in place rather than uploaded during this lifecycle pass.
	Reused bool    `json:"reused,omitempty"`
	Reason *Reason `json:"reason,omitempty"`
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
	LoadMetadata(context.Context) (SignedReleaseMetadata, error)
	LoadArtifact(context.Context, Artifact) ([]byte, error)
}

// OwnershipAction is carried with a settings replacement and is deliberately
// not a standalone UI mutation.  That prevents an abandoned settings draft
// from forgetting ownership of a still-configured remote helper.
type OwnershipAction string

const (
	OwnershipActionNone      OwnershipAction = ""
	OwnershipActionRelease   OwnershipAction = "release"
	OwnershipActionUninstall OwnershipAction = "uninstall"
)

type lifecycleFactory func(alias string) (Lifecycle, error)

type helperTarget struct {
	path    string
	version string
	state   LifecycleState
}

type helperProbeState string

const (
	helperProbeUnknown  helperProbeState = "unknown"
	helperProbeProbing  helperProbeState = "probing"
	helperProbeReady    helperProbeState = "ready"
	helperProbeFallback helperProbeState = "fallback"
)

func lifecycleStatus(result LifecycleResult) HelperStatus {
	status := HelperStatus{
		State: result.State, Version: result.Artifact.Version,
		Compatible: result.CanExecute, Fallback: result.Fallback, Reused: result.Reused,
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

type unavailableReleaseProvider struct{}

func (unavailableReleaseProvider) LoadMetadata(context.Context) (SignedReleaseMetadata, error) {
	return SignedReleaseMetadata{}, errHelperReleaseUnavailable
}

func (unavailableReleaseProvider) LoadArtifact(context.Context, Artifact) ([]byte, error) {
	return nil, errHelperReleaseUnavailable
}

// NewProductionManager centralizes the production wiring. This build has no
// approved embedded release keys or publication endpoint yet, so it carries a
// non-nil, deliberately unavailable provider and disables installation in the
// API/UI. Replacing the provider and trust constants is a release operation,
// not a frontend-controlled setting.
func NewProductionManager() (*Manager, error) {
	trust := TrustStore{
		Keys: map[string]ed25519.PublicKey{}, RevokedKeys: map[string]bool{},
		MinimumSequence: 0,
		Sequences:       &FileMetadataSequenceStore{Path: filepath.Join(settings.Home(), "helper-release", "sequence")},
	}
	return NewManager(Options{
		ReleaseProvider: unavailableReleaseProvider{}, Trust: trust,
		HelperInstallationAvailable: false,
	}), nil
}

// startHelperDiscoveryLocked starts only a read-only verification pass. The
// Lifecycle receives empty consent, so it can authenticate metadata, probe the
// platform, inspect files, and negotiate capabilities but can never upload or
// modify a remote helper.
func (manager *Manager) startHelperDiscoveryLocked() {
	if manager.helperProbe != helperProbeUnknown || manager.cfg == nil || manager.lifeCtx == nil {
		return
	}
	if !manager.helperInstallationAvailable || manager.releaseProvider == nil || manager.lifecycleFactory == nil {
		status := unavailableHelperStatus(errHelperReleaseUnavailable)
		manager.helper = &status
		manager.helperProbe = helperProbeFallback
		return
	}
	manager.helperProbe = helperProbeProbing
	status := HelperStatus{State: LifecycleSFTP}
	manager.helper = &status
	config := *manager.cfg
	go manager.runHelperDiscovery(manager.lifeCtx, config)
}

// InspectHelper starts (or reports) the non-mutating discovery pass for the
// setup screen. It never waits on SSH while holding the manager lock.
func (manager *Manager) InspectHelper() Health {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg != nil && manager.cfg.Enabled {
		manager.startHelperDiscoveryLocked()
	}
	return manager.healthLocked(manager.lastRequestedMs)
}

func (manager *Manager) runHelperDiscovery(ctx context.Context, config settings.RemoteSettings) {
	manager.mu.Lock()
	provider, factory := manager.releaseProvider, manager.lifecycleFactory
	manager.mu.Unlock()
	document, err := provider.LoadMetadata(ctx)
	var result LifecycleResult
	if err == nil {
		var lifecycle Lifecycle
		lifecycle, err = factory(config.SSHAlias)
		if err == nil {
			result = lifecycle.SetupWithLoader(ctx, document, Consent{}, provider.LoadArtifact)
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || manager.cfg.ID != config.ID || manager.cfg.SSHAlias != config.SSHAlias || ctx.Err() != nil {
		return
	}
	if err != nil {
		status := unavailableHelperStatus(err)
		manager.helper = &status
		manager.helperProbe = helperProbeFallback
	} else {
		status := lifecycleStatus(result)
		manager.helper = &status
		if !useSFTPFallbackAfterDiscovery(result) {
			manager.helperTarget = &helperTarget{path: result.Path, version: result.Artifact.Version, state: result.State}
			manager.helperVersion = result.Artifact.Version
			// This records ownership locally, never remote execution authority.
			if storeErr := manager.cache.StoreHelperVersion(config.ID, result.Artifact.Version, config.SSHAlias); storeErr != nil {
				failed := unavailableHelperStatus(fmt.Errorf("%w: %v", ErrHelperInstallation, storeErr))
				manager.helper = &failed
				manager.helperTarget = nil
				manager.helperProbe = helperProbeFallback
			} else {
				manager.helperProbe = helperProbeReady
			}
		} else {
			manager.helperTarget = nil
			manager.helperProbe = helperProbeFallback
		}
	}
	// The first ListView window was retained while discovery ran. Only now may
	// the normal refresh choose helper or explicit SFTP fallback.
	manager.maybeStartRefreshLocked(manager.lastRequestedMs, false)
}

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

	if !manager.helperInstallationAvailable || provider == nil || factory == nil {
		manager.mu.Lock()
		status := unavailableHelperStatus(errHelperReleaseUnavailable)
		manager.helper = &status
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health
	}
	document, err := provider.LoadMetadata(ctx)
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
	result := lifecycle.SetupWithLoader(ctx, document, consent, provider.LoadArtifact)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || manager.cfg.ID != config.ID || manager.cfg.SSHAlias != config.SSHAlias {
		return manager.healthLocked(manager.lastRequestedMs)
	}
	status := lifecycleStatus(result)
	manager.helper = &status
	if result.CanExecute {
		if err := manager.cache.StoreHelperVersion(config.ID, result.Artifact.Version, config.SSHAlias); err != nil {
			status = unavailableHelperStatus(fmt.Errorf("%w: %v", ErrHelperInstallation, err))
			manager.helper = &status
			manager.helperTarget = nil
			return manager.healthLocked(manager.lastRequestedMs)
		}
		manager.helperTarget = &helperTarget{path: result.Path, version: result.Artifact.Version, state: result.State}
		manager.helperVersion = result.Artifact.Version
		manager.helperProbe = helperProbeReady
	} else {
		manager.helperTarget = nil
		manager.helperProbe = helperProbeFallback
	}
	return manager.healthLocked(manager.lastRequestedMs)
}

// ReleaseHelperOwnership explicitly forgets local ownership without modifying
// Linux. It is the only path that permits an alias change while intentionally
// leaving a helper installed.
func (manager *Manager) ReleaseHelperOwnership() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || manager.helperVersion == "" {
		return nil
	}
	if err := manager.cache.RemoveHelperVersion(manager.cfg.ID); err != nil {
		return err
	}
	manager.helperVersion = ""
	manager.helperTarget = nil
	status := HelperStatus{State: LifecycleSFTP, Fallback: true, Reason: reasonPtr(ReasonHelperMissing)}
	manager.helper = &status
	manager.helperProbe = helperProbeFallback
	return nil
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
	document, err := provider.LoadMetadata(ctx)
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

// HelperTestResult reports the result of the small setup operation itself.
// Health remains board/cache health and can be incomplete when its durable
// snapshot covers a different time window than this non-persisting test.
type HelperTestResult struct {
	Health    Health
	Succeeded bool
	Reason    *Reason
}

// TestHelper performs a small, non-persisting collection after setup. It uses
// the verified target already held by the manager, so a test can never execute
// a frontend- or response-supplied path. The normal requested-window refresh
// remains responsible for durable cache commits.
func (manager *Manager) TestHelper(ctx context.Context) HelperTestResult {
	manager.mu.Lock()
	if manager.cfg == nil || !manager.cfg.Enabled || manager.helperTarget == nil {
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		reason := ReasonHelperMissing
		return HelperTestResult{Health: health, Reason: &reason}
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
		health := manager.healthLocked(manager.lastRequestedMs)
		return HelperTestResult{Health: health, Reason: health.Reason}
	}
	manager.transport = TransportHelper
	manager.metrics = metricsFor(result)
	if err != nil {
		reason := classifyHelperError(err)
		manager.applyFailureLocked(reason, result.Stderr)
		return HelperTestResult{Health: manager.healthLocked(manager.lastRequestedMs), Reason: reasonPtr(reason)}
	}
	if reason := limitedResultReason(result); reason != nil {
		manager.state = StateLimited
		manager.complete = false
		manager.reason = reasonPtr(*reason)
		manager.errorCopy = genericErrorCopy(*reason)
		return HelperTestResult{Health: manager.healthLocked(manager.lastRequestedMs), Reason: reasonPtr(*reason)}
	}
	manager.state = StateOK
	manager.complete = true
	manager.reason = nil
	manager.errorCopy = ""
	manager.diagnostic = ""
	return HelperTestResult{Health: manager.healthLocked(manager.lastRequestedMs), Succeeded: true}
}

// ValidateSettingsChangeWithOwnershipAction permits a replacement only when
// the requested ownership action is bundled with that replacement.  The
// caller must subsequently call ApplyOwnershipAction before ApplySettings.
func (manager *Manager) ValidateSettingsChangeWithOwnershipAction(next *settings.RemoteSettings, action OwnershipAction) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if action != OwnershipActionRelease && action != OwnershipActionUninstall {
		return ErrHelperOwnershipConflict
	}
	if manager.cfg == nil || manager.helperVersion == "" {
		return ErrHelperOwnershipConflict
	}
	if next != nil && manager.cfg.ID == next.ID && manager.cfg.SSHAlias == next.SSHAlias {
		return ErrHelperOwnershipConflict
	}
	// Still validate the proposed settings shape, but ownership is released by
	// the explicitly requested transaction rather than by this inspection.
	if next != nil && (!settings.ValidRemoteID(next.ID) || !settings.ValidSSHAlias(next.SSHAlias)) {
		return ErrInvalidRemoteSettings
	}
	return nil
}

// ApplyOwnershipAction executes the already-authorized operation while the
// manager still owns the old settings. It is intentionally used only by the
// settings-save transaction in cmd/coslash.
func (manager *Manager) ApplyOwnershipAction(ctx context.Context, action OwnershipAction) error {
	switch action {
	case OwnershipActionRelease:
		return manager.ReleaseHelperOwnership()
	case OwnershipActionUninstall:
		return manager.UninstallHelper(ctx)
	default:
		return ErrHelperOwnershipConflict
	}
}

// helperRefreshFunc is kept as a named boundary so manager tests can substitute
// a deterministic helper without accepting arbitrary remote paths.
type helperRefreshFunc func(context.Context, string, int64, time.Time, CachedSnapshotV2, helperTarget) (refreshOutcome, error)
