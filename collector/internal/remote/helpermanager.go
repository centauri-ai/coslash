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
	path     string
	version  string
	state    LifecycleState
	artifact Artifact
}

type helperVerifyFunc func(context.Context, string, helperTarget) error

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

// NewProductionManager centralizes the production wiring. Release builds
// compile both Linux helpers into the Coslash executable; ordinary go test and
// go run builds deliberately use the unavailable provider so generated binary
// assets are never required in the source tree.
func NewProductionManager() (*Manager, error) {
	provider, embedded := newProductionHelperReleaseProvider()
	trust := TrustStore{
		Keys: map[string]ed25519.PublicKey{}, RevokedKeys: map[string]bool{},
		MinimumSequence: 0,
		Sequences:       &FileMetadataSequenceStore{Path: filepath.Join(settings.Home(), "helper-release", "sequence")},
		AllowEmbedded:   embedded,
	}
	return NewManager(Options{
		ReleaseProvider: provider, Trust: trust,
		HelperInstallationAvailable: embedded,
	}), nil
}

// startHelperDiscoveryLocked verifies a helper or updates one previously
// installed with this source's recorded ownership.
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
	autoUpdate := manager.helperVersion != "" && !manager.helperOwnershipCorrupt
	if autoUpdate {
		manager.helperSetup = true
	}
	go manager.runHelperDiscovery(manager.lifeCtx, config, autoUpdate)
}

// InspectHelper starts (or reports) the background helper check for the setup
// screen. It never waits on SSH while holding the manager lock.
func (manager *Manager) InspectHelper() Health {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg != nil && manager.cfg.Enabled {
		manager.startHelperDiscoveryLocked()
	}
	return manager.healthLocked(manager.lastRequestedMs)
}

func (manager *Manager) runHelperDiscovery(ctx context.Context, config settings.RemoteSettings, autoUpdate bool) {
	if autoUpdate {
		defer func() {
			manager.mu.Lock()
			manager.helperSetup = false
			manager.mu.Unlock()
		}()
	}
	timeout := DefaultConnectTimeout
	if autoUpdate {
		timeout = DefaultHelperAutoUpdateTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	manager.mu.Lock()
	provider, factory, previousVersion := manager.releaseProvider, manager.lifecycleFactory, manager.helperVersion
	manager.mu.Unlock()
	document, err := provider.LoadMetadata(probeCtx)
	var result LifecycleResult
	var lifecycle Lifecycle
	if err == nil {
		lifecycle, err = factory(config.SSHAlias)
		if err == nil {
			consent := Consent{}
			if autoUpdate {
				consent = Consent{Install: true, Upgrade: true}
			}
			result = lifecycle.SetupWithLoader(probeCtx, document, consent, provider.LoadArtifact)
			if autoUpdate && retryableSetupTransportFailure(result) {
				manager.resetControlMaster(config.SSHAlias)
				if retryLifecycle, retryErr := factory(config.SSHAlias); retryErr == nil {
					result = retryLifecycle.SetupWithLoader(probeCtx, document, consent, provider.LoadArtifact)
					lifecycle = retryLifecycle
				}
			}
		}
	}
	if result.CanExecute && autoUpdate && previousVersion != "" && previousVersion != result.Artifact.Version && lifecycle.Remote != nil {
		if previousPath, pathErr := helperPath(previousVersion); pathErr == nil {
			_ = lifecycle.Remote.RemoveExact(probeCtx, previousPath)
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || manager.cfg.ID != config.ID || manager.cfg.SSHAlias != config.SSHAlias || ctx.Err() != nil {
		return
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || sshErrorStderr(err) != "" {
			manager.lastCheckedAt = int64Ptr(manager.now().UnixMilli())
			manager.helperProbe = helperProbeFallback
			status := unavailableHelperStatus(err)
			manager.helper = &status
			manager.applyFailureLocked(classifyError(err), sshErrorStderr(err))
			return
		}
		status := unavailableHelperStatus(err)
		manager.helper = &status
		manager.helperProbe = helperProbeFallback
	} else {
		status := lifecycleStatus(result)
		manager.helper = &status
		if !useSFTPFallbackAfterDiscovery(result) {
			manager.helperTarget = &helperTarget{path: result.Path, version: result.Artifact.Version, state: result.State, artifact: result.Artifact}
			// This records ownership locally, never remote execution authority.
			if storeErr := manager.cache.StoreHelperVersion(config.ID, result.Artifact.Version, config.SSHAlias); storeErr != nil {
				failed := unavailableHelperStatus(fmt.Errorf("%w: %v", ErrHelperInstallation, storeErr))
				manager.helper = &failed
				manager.helperTarget = nil
				manager.helperVersion = ""
				manager.helperProbe = helperProbeFallback
			} else {
				manager.helperVersion = result.Artifact.Version
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
	health, _ := manager.setupHelper(ctx, "", consent)
	return health
}

// SetupHelperForAlias binds consented setup to the alias the user tested. It
// rejects an unsaved settings draft before a lifecycle can contact a host.
func (manager *Manager) SetupHelperForAlias(ctx context.Context, alias string, consent Consent) (Health, error) {
	return manager.setupHelper(ctx, alias, consent)
}

func (manager *Manager) SetupProgress() SetupProgress {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.setupProgress
}

func (manager *Manager) setupHelper(ctx context.Context, expectedAlias string, consent Consent) (Health, error) {
	manager.mu.Lock()
	if manager.cfg == nil {
		manager.mu.Unlock()
		return Health{State: StateDisabled, Complete: true, Reason: reasonPtr(ReasonDisabled)}, nil
	}
	if expectedAlias != "" && manager.cfg.SSHAlias != expectedAlias {
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health, ErrHelperAliasMismatch
	}
	if manager.helperSetup {
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health, ErrHelperSetupInProgress
	}
	config := *manager.cfg
	previousVersion := manager.helperVersion
	provider, factory := manager.releaseProvider, manager.lifecycleFactory
	// Keep this host identity reserved until setup has either recorded
	// ownership or finished without installing anything. SetupWithLoader can
	// release this mutex while uploading the helper, so ApplySettings must not
	// replace the alias in that interval.
	manager.helperSetup = true
	manager.setupProgress = SetupProgressChecking
	manager.mu.Unlock()
	defer func() {
		manager.mu.Lock()
		manager.helperSetup = false
		manager.setupProgress = SetupProgressIdle
		manager.mu.Unlock()
	}()

	if !manager.helperInstallationAvailable || provider == nil || factory == nil {
		manager.mu.Lock()
		status := unavailableHelperStatus(errHelperReleaseUnavailable)
		manager.helper = &status
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health, nil
	}
	document, err := provider.LoadMetadata(ctx)
	if err != nil {
		manager.mu.Lock()
		status := unavailableHelperStatus(err)
		manager.helper = &status
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health, nil
	}
	lifecycle, err := factory(config.SSHAlias)
	if err != nil {
		manager.mu.Lock()
		status := unavailableHelperStatus(err)
		manager.helper = &status
		health := manager.healthLocked(manager.lastRequestedMs)
		manager.mu.Unlock()
		return health, nil
	}
	lifecycle.Progress = manager.setSetupProgress
	result := lifecycle.SetupWithLoader(ctx, document, consent, provider.LoadArtifact)
	activeLifecycle := lifecycle
	if retryableSetupTransportFailure(result) {
		manager.resetControlMaster(config.SSHAlias)
		if retryLifecycle, retryErr := factory(config.SSHAlias); retryErr == nil {
			retryLifecycle.Progress = manager.setSetupProgress
			result = retryLifecycle.SetupWithLoader(ctx, document, consent, provider.LoadArtifact)
			activeLifecycle = retryLifecycle
		}
	}
	if result.CanExecute && previousVersion != "" && previousVersion != result.Artifact.Version && activeLifecycle.Remote != nil {
		if previousPath, pathErr := helperPath(previousVersion); pathErr == nil {
			_ = activeLifecycle.Remote.RemoveExact(ctx, previousPath)
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || manager.cfg.ID != config.ID || manager.cfg.SSHAlias != config.SSHAlias {
		return manager.healthLocked(manager.lastRequestedMs), nil
	}
	status := lifecycleStatus(result)
	manager.helper = &status
	if result.CanExecute {
		if err := manager.cache.StoreHelperVersion(config.ID, result.Artifact.Version, config.SSHAlias); err != nil {
			status = unavailableHelperStatus(fmt.Errorf("%w: %v", ErrHelperInstallation, err))
			manager.helper = &status
			manager.helperTarget = nil
			return manager.healthLocked(manager.lastRequestedMs), nil
		}
		manager.helperTarget = &helperTarget{path: result.Path, version: result.Artifact.Version, state: result.State, artifact: result.Artifact}
		manager.helperVersion = result.Artifact.Version
		manager.helperProbe = helperProbeReady
	} else {
		manager.helperTarget = nil
		manager.helperProbe = helperProbeFallback
	}
	return manager.healthLocked(manager.lastRequestedMs), nil
}

func (manager *Manager) setSetupProgress(progress SetupProgress) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.helperSetup {
		manager.setupProgress = progress
	}
}

func retryableSetupTransportFailure(result LifecycleResult) bool {
	if result.CanExecute || result.State != LifecycleSFTP || result.Reason == nil {
		return false
	}
	if errors.Is(result.Reason, ErrHelperNoExec) || errors.Is(result.Reason, ErrHelperVerification) ||
		errors.Is(result.Reason, ErrUnsupportedHelperPlatform) {
		return false
	}
	return errors.Is(result.Reason, context.DeadlineExceeded) || sshErrorStderr(result.Reason) != "" ||
		classifyError(result.Reason) == ReasonConnectionFailed
}

// ReleaseHelperOwnership explicitly forgets local ownership without modifying
// Linux. It is the only path that permits an alias change while intentionally
// leaving a helper installed.
func (manager *Manager) ReleaseHelperOwnership() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || (manager.helperVersion == "" && !manager.helperOwnershipCorrupt) {
		return nil
	}
	if err := manager.cache.RemoveHelperVersion(manager.cfg.ID); err != nil {
		return err
	}
	manager.helperVersion = ""
	manager.helperOwnershipCorrupt = false
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
	if manager.helperOwnershipCorrupt {
		manager.mu.Unlock()
		return ErrHelperOwnershipCorrupt
	}
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
// a frontend- or response-supplied path. Failure must not schedule refresh
// backoff, flip transport, or rewrite durable board failure state; only a
// successful probe may advertise helper transport. The normal requested-window
// refresh remains responsible for durable cache commits and retry policy.
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
	verify := manager.helperVerify
	now := manager.now()
	manager.mu.Unlock()

	since := now.Add(-24 * time.Hour).UnixMilli()
	err := verify(ctx, config.SSHAlias, target)
	var result refreshOutcome
	if err == nil {
		result, err = refresh(ctx, config.SSHAlias, since, now, baseline, target)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || manager.cfg.ID != config.ID || manager.cfg.SSHAlias != config.SSHAlias {
		health := manager.healthLocked(manager.lastRequestedMs)
		return HelperTestResult{Health: health, Reason: health.Reason}
	}
	if err != nil {
		reason := classifyHelperError(err)
		return HelperTestResult{Health: manager.healthLocked(manager.lastRequestedMs), Reason: reasonPtr(reason)}
	}
	// This is deliberately a non-persisting probe. Do not alter state,
	// completeness, or sessions: those describe the last durable snapshot and
	// could otherwise make stale cached data aggregate-eligible. A successful
	// probe may advertise its verified transport and bounded metrics.
	manager.transport = TransportHelper
	manager.metrics = metricsFor(result)
	if reason := limitedResultReason(result); reason != nil {
		return HelperTestResult{
			Health:    manager.healthLocked(manager.lastRequestedMs),
			Succeeded: *reason == ReasonNoSupportedData,
			Reason:    reasonPtr(*reason),
		}
	}
	return HelperTestResult{Health: manager.healthLocked(manager.lastRequestedMs), Succeeded: true}
}

// ValidateSettingsChangeWithOwnershipAction permits a replacement only when
// the requested ownership action is bundled with that replacement.  The
// caller must subsequently call ApplyOwnershipAction before ApplySettings.
func (manager *Manager) ValidateSettingsChangeWithOwnershipAction(next *settings.RemoteSettings, action OwnershipAction) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.helperSetup {
		return ErrHelperSetupInProgress
	}
	if action != OwnershipActionRelease && action != OwnershipActionUninstall {
		return ErrHelperOwnershipConflict
	}
	if next == nil && action == OwnershipActionRelease {
		return nil
	}
	if manager.cfg == nil || (manager.helperVersion == "" && !manager.helperOwnershipCorrupt) {
		return ErrHelperOwnershipConflict
	}
	if manager.helperOwnershipCorrupt && action == OwnershipActionUninstall {
		return ErrHelperOwnershipCorrupt
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
