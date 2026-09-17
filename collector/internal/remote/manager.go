package remote

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

var (
	ErrInvalidRemoteSettings    = errors.New("invalid remote settings")
	ErrHelperOwnershipConflict  = errors.New("helper ownership must be explicitly released or uninstalled before changing SSH alias")
	ErrHelperAliasMismatch      = errors.New("helper setup alias does not match configured SSH alias")
	ErrHelperSetupInProgress    = errors.New("helper setup is running for the configured SSH alias")
	ErrRemoteSessionActive      = errors.New("remote session already has an active writer")
	ErrRemoteSessionUnavailable = errors.New("remote session details are unavailable")
	ErrRemoteSessionOversized   = errors.New("remote session details exceed the collection size limit")
	ErrRemoteRevisionNotFound   = errors.New("remote session revision is unavailable")
	ErrRemoteRecordCorrupt      = errors.New("remote session record is corrupt")
	ErrRemoteChangeNotFound     = errors.New("remote file change is unavailable")
)

type LaunchBlockReason string

const (
	LaunchBlockMissingDetails LaunchBlockReason = "missing_details"
	LaunchBlockOversized      LaunchBlockReason = "oversized"
)

type SessionKey struct {
	SourceID        string
	Agent           string
	SourceSessionID string
}

type IndexedSession struct {
	Key                   SessionKey
	SourceLabel           string
	Session               *session.Session
	EligibleForAggregates bool
	DisplayStale          bool
	LastSeenStatus        *string
	Launchable            bool
	LaunchBlockReason     LaunchBlockReason
	RevisionID            string
}

type remoteSessionKey struct{ Agent, ID string }

type ListResult struct {
	Sessions []IndexedSession
	Health   Health
}

// refreshOutcome is what one incremental SFTP refresh produces: a durable v2
// snapshot ready to store as-is, and the sessions composed from it.
type refreshOutcome struct {
	Snapshot  CachedSnapshotV2
	Sessions  []*session.Session
	Failures  []error
	Stderr    string
	RoundTrip time.Duration
	Metrics   CollectionMetrics
	Reason    *Reason
}

type probeResult struct {
	Stderr    string
	RoundTrip time.Duration
}

// refreshFunc receives the current cached generation as baseline so it can
// skip re-collecting families whose fingerprint has not changed.
type refreshFunc func(ctx context.Context, alias string, since int64, now time.Time, baseline CachedSnapshotV2) (refreshOutcome, error)
type probeFunc func(ctx context.Context, alias string) (probeResult, error)
type openFunc func(context.Context, string, OpenOptions) (*Session, error)

type Manager struct {
	mu sync.Mutex

	cache                       *Cache
	now                         func() time.Time
	refresh                     refreshFunc
	helperRefresh               helperRefreshFunc
	test                        probeFunc
	releaseProvider             HelperReleaseProvider
	lifecycleFactory            lifecycleFactory
	trust                       TrustStore
	helperInstallationAvailable bool

	cfg        *settings.RemoteSettings
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	// snapshot is nil until a cache (v1 or v2) has been loaded or a refresh has
	// committed. legacyStale is true while snapshot only carries display fields
	// inherited from a v1 cache: its Families stay empty so the next refresh
	// starts from an empty baseline rather than reinterpreting v1 fingerprints.
	snapshot               *CachedSnapshotV2
	legacyStale            bool
	sessions               []*session.Session
	familyStale            map[remoteSessionKey]bool
	state                  State
	reason                 *Reason
	complete               bool
	errorCopy              string
	diagnostic             string
	refreshing             bool
	lastRequestedMs        int64
	failures               int
	nextRetryAt            time.Time
	nextHelperProbeAt      time.Time
	lastManualRetryAt      time.Time
	lastSuccessAt          *int64
	lastCheckedAt          *int64
	transport              Transport
	helper                 *HelperStatus
	helperTarget           *helperTarget
	helperVerify           helperVerifyFunc
	resetControlMaster     func(string)
	helperSetup            bool
	helperAutoSetup        bool
	helperVersion          string
	helperOwnershipCorrupt bool
	helperProbe            helperProbeState
	metrics                CollectionMetrics
}

type Options struct {
	Cache   *Cache
	Now     func() time.Time
	Refresh refreshFunc
	// HelperRefresh is only invoked after SetupHelper creates a verified target.
	// Supplying it is useful for deterministic manager tests.
	HelperRefresh               helperRefreshFunc
	HelperVerify                helperVerifyFunc
	ResetControlMaster          func(string)
	ReleaseProvider             HelperReleaseProvider
	LifecycleFactory            lifecycleFactory
	Trust                       TrustStore
	HelperInstallationAvailable bool
	Test                        probeFunc
	Open                        openFunc
}

func NewManager(options Options) *Manager {
	cache := options.Cache
	if cache == nil {
		cache = NewCache("")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	open := options.Open
	if open == nil {
		open = OpenSession
	}
	refresh := options.Refresh
	if refresh == nil {
		refresh = func(ctx context.Context, alias string, since int64, now time.Time, baseline CachedSnapshotV2) (refreshOutcome, error) {
			return refreshIncrementalWithOpen(ctx, alias, since, now, baseline, open)
		}
	}
	test := options.Test
	if test == nil {
		test = func(ctx context.Context, alias string) (probeResult, error) {
			return probeSFTPWithOpen(ctx, alias, open)
		}
	}
	helperRefresh := options.HelperRefresh
	if helperRefresh == nil {
		helperRefresh = func(ctx context.Context, alias string, since int64, now time.Time, baseline CachedSnapshotV2, target helperTarget) (refreshOutcome, error) {
			return helperRefreshWithOpen(ctx, alias, since, now, baseline, target, OpenOptions{})
		}
	}
	factory := options.LifecycleFactory
	if factory == nil && options.ReleaseProvider != nil {
		factory = defaultLifecycleFactory(options.Trust)
	}
	helperVerify := options.HelperVerify
	if helperVerify == nil {
		helperVerify = func(ctx context.Context, alias string, target helperTarget) error {
			if factory == nil {
				return ErrHelperVerification
			}
			lifecycle, err := factory(alias)
			if err != nil {
				return err
			}
			return lifecycle.VerifyExecution(ctx, target.path, target.artifact)
		}
	}
	resetControlMaster := options.ResetControlMaster
	if resetControlMaster == nil {
		resetControlMaster = exitControlMasterBestEffort
	}
	return &Manager{
		cache: cache, now: now, refresh: refresh, helperRefresh: helperRefresh, test: test,
		helperVerify:       helperVerify,
		resetControlMaster: resetControlMaster,
		releaseProvider:    options.ReleaseProvider, lifecycleFactory: factory,
		trust:                       options.Trust,
		helperInstallationAvailable: options.HelperInstallationAvailable,
		state:                       StateDisabled, complete: true, transport: TransportSFTP,
		helperProbe: helperProbeFallback,
	}
}

func (manager *Manager) ApplySettings(remote *settings.RemoteSettings) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.helperSetup && remoteTargetChanged(manager.cfg, remote) {
		return ErrHelperSetupInProgress
	}
	corruptOwnership := false
	if remote != nil {
		ownership, owned, err := manager.cache.LoadHelperOwnership(remote.ID)
		if errors.Is(err, ErrHelperOwnershipLegacy) && owned {
			// The old record was created while this was the only configured host.
			// Bind it atomically to the persisted alias before any later alias
			// comparison can be allowed.
			if err := manager.cache.StoreHelperVersion(remote.ID, ownership.Version, remote.SSHAlias); err != nil {
				return err
			}
		} else if errors.Is(err, ErrHelperOwnershipCorrupt) {
			corruptOwnership = true
		} else if err != nil {
			return err
		}
	}
	if corruptOwnership {
		if !settings.ValidRemoteID(remote.ID) || !settings.ValidSSHAlias(remote.SSHAlias) {
			return ErrInvalidRemoteSettings
		}
	} else if err := manager.validateSettingsLocked(remote); err != nil {
		return err
	}
	if remote == nil {
		return manager.removeLocked()
	}
	ownership, owned, err := manager.cache.LoadHelperOwnership(remote.ID)
	if errors.Is(err, ErrHelperOwnershipCorrupt) {
		corruptOwnership = true
		ownership = helperOwnership{}
		owned = false
	} else if err != nil {
		return err
	}
	if manager.cfg != nil && manager.cfg.ID != remote.ID {
		if err := manager.removeLocked(); err != nil {
			return err
		}
	}
	previous := manager.cfg
	aliasChanged := previous != nil && previous.SSHAlias != remote.SSHAlias
	if aliasChanged {
		exitControlMasterBestEffort(previous.SSHAlias)
		if err := manager.cache.RemoveSource(remote.ID); err != nil {
			return err
		}
		manager.helper = nil
		manager.helperTarget = nil
	}
	copyConfig := *remote
	manager.cfg = &copyConfig
	if owned {
		manager.helperVersion = ownership.Version
	} else {
		manager.helperVersion = ""
	}
	manager.helperOwnershipCorrupt = corruptOwnership
	if !remote.Enabled {
		manager.cancelLifeLocked()
		manager.refreshing = false
		manager.state = StateDisabled
		manager.reason = reasonPtr(ReasonDisabled)
		manager.complete = true
		manager.errorCopy = ""
		manager.transport = TransportSFTP
		exitControlMasterBestEffort(remote.SSHAlias)
		return nil
	}
	restart := previous == nil || !previous.Enabled || previous.SSHAlias != remote.SSHAlias
	if restart {
		if err := manager.loadCacheLocked(); err != nil {
			return err
		}
		manager.startLifeLocked()
		manager.refreshing = false
		if manager.snapshot != nil {
			manager.state = StateStale
		} else {
			manager.state = StateConnecting
		}
		manager.reason = reasonPtr(ReasonInitialRefresh)
		manager.complete = false
		manager.failures = 0
		manager.nextRetryAt = time.Time{}
		manager.lastManualRetryAt = time.Time{}
		manager.helperTarget = nil
		manager.helperProbe = helperProbeUnknown
		// Initial collection waits for ListView's first requested window
		// instead of starting eagerly with since=0.
	}
	if corruptOwnership {
		manager.helperTarget = nil
		manager.helperProbe = helperProbeFallback
		manager.helper = &HelperStatus{
			State: LifecycleVerificationError, Fallback: true,
			Reason: reasonPtr(ReasonHelperVerification),
		}
	}
	return nil
}

// ValidateSettingsChange checks remote-helper ownership before settings.json
// is written, so a rejected alias replacement cannot persist an ambiguous
// local/remote ownership state.
func (manager *Manager) ValidateSettingsChange(remote *settings.RemoteSettings) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.validateSettingsLocked(remote)
}

func (manager *Manager) validateSettingsLocked(remote *settings.RemoteSettings) error {
	if manager.helperSetup && remoteTargetChanged(manager.cfg, remote) {
		return ErrHelperSetupInProgress
	}
	if remote == nil {
		if manager.helperVersion != "" || manager.helperOwnershipCorrupt {
			return ErrHelperOwnershipConflict
		}
		return nil
	}
	if !settings.ValidRemoteID(remote.ID) || !settings.ValidSSHAlias(remote.SSHAlias) {
		return ErrInvalidRemoteSettings
	}
	ownership, owned, err := manager.cache.LoadHelperOwnership(remote.ID)
	if err != nil {
		return err
	}
	if manager.helperOwnershipCorrupt {
		return ErrHelperOwnershipCorrupt
	}
	if owned && ownership.Alias != remote.SSHAlias {
		return ErrHelperOwnershipConflict
	}
	if manager.cfg != nil && manager.cfg.ID != remote.ID && manager.helperVersion != "" {
		return ErrHelperOwnershipConflict
	}
	return nil
}

func remoteTargetChanged(current, next *settings.RemoteSettings) bool {
	if current == nil || next == nil {
		return current != next
	}
	return current.ID != next.ID || current.SSHAlias != next.SSHAlias
}

func (manager *Manager) ListView(remoteSinceMs int64) ListResult {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	remoteSinceMs = max(0, remoteSinceMs)
	manager.lastRequestedMs = remoteSinceMs
	if manager.cfg == nil {
		return ListResult{Health: Health{State: StateDisabled, Complete: true, Reason: reasonPtr(ReasonDisabled)}}
	}
	if !manager.cfg.Enabled {
		return ListResult{Health: manager.healthLocked(remoteSinceMs)}
	}
	if manager.helperProbe == helperProbeFallback && manager.helperVersion != "" &&
		!manager.nextHelperProbeAt.IsZero() && !manager.now().Before(manager.nextHelperProbeAt) {
		manager.helperProbe = helperProbeUnknown
	}
	if manager.helperProbe == helperProbeUnknown {
		manager.startHelperDiscoveryLocked()
	}
	if manager.helperProbe == helperProbeProbing {
		return manager.listResultLocked(remoteSinceMs)
	}
	if manager.snapshot == nil || age(manager.snapshot.FetchedAtMs, manager.now()) >= FreshnessInterval ||
		manager.snapshot.CoverageSinceMs > remoteSinceMs {
		manager.maybeStartRefreshLocked(remoteSinceMs, false)
	}
	return manager.listResultLocked(remoteSinceMs)
}

func (manager *Manager) listResultLocked(remoteSinceMs int64) ListResult {
	sessions := manager.sessionsLocked(remoteSinceMs)
	return ListResult{Sessions: sessions, Health: manager.healthLockedWithSessionCount(remoteSinceMs, len(sessions))}
}

// Retry starts one manual refresh unless another one is running or the
// caller retried too recently. The bool reports whether a refresh started.
func (manager *Manager) Retry() (Health, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || !manager.cfg.Enabled {
		return manager.healthLocked(manager.lastRequestedMs), false
	}
	if manager.refreshing || (!manager.lastManualRetryAt.IsZero() &&
		manager.now().Sub(manager.lastManualRetryAt) < ManualRetryCooldown) {
		return manager.healthLocked(manager.lastRequestedMs), false
	}
	manager.lastManualRetryAt = manager.now()
	manager.maybeStartRefreshLocked(manager.lastRequestedMs, true)
	return manager.healthLocked(manager.lastRequestedMs), true
}

func (manager *Manager) TestAlias(ctx context.Context, alias string) (Health, error) {
	if !settings.ValidSSHAlias(alias) {
		return Health{}, ErrInvalidRemoteSettings
	}
	result, err := manager.test(ctx, alias)
	health := Health{
		Label: alias, Complete: true,
		RoundTripMs: int64Ptr(result.RoundTrip.Milliseconds()),
		Transport:   TransportSFTP,
	}
	if err != nil {
		reason := classifyError(err)
		health.State = StateError
		health.Complete = false
		health.Reason = reasonPtr(reason)
		health.Error = genericErrorCopy(reason)
		return health, nil
	}
	health.State = StateOK
	// A successful settings test of the active host means SSH works again —
	// clear backoff and collect so the board strip updates without a manual Retry.
	manager.recoverAfterSuccessfulTest(alias)
	return health, nil
}

func (manager *Manager) recoverAfterSuccessfulTest(alias string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || !manager.cfg.Enabled || manager.cfg.SSHAlias != alias {
		return
	}
	manager.failures = 0
	manager.nextRetryAt = time.Time{}
	manager.lastManualRetryAt = time.Time{}
	manager.maybeStartRefreshLocked(manager.lastRequestedMs, true)
}

func (manager *Manager) DiagnosticsHealth() Health {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.healthLocked(manager.lastRequestedMs)
}

// LaunchSession returns the current remote session and SSH alias only while
// the host's most recent collection completed successfully.
func (manager *Manager) LaunchSession(sourceID, agent, sessionID, mode string) (*session.Session, string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || !manager.cfg.Enabled || manager.cfg.ID != sourceID ||
		(manager.state != StateOK && manager.state != StateLimited) {
		return nil, "", nil
	}
	for _, item := range manager.sessions {
		if item.Agent == agent && item.ID == sessionID {
			switch launchBlockReason(manager.snapshot, item) {
			case LaunchBlockOversized:
				return nil, "", ErrRemoteSessionOversized
			case LaunchBlockMissingDetails:
				return nil, "", ErrRemoteSessionUnavailable
			}
			if mode == launch.ResumeSession && item.Status != nil && (*item.Status == "busy" || *item.Status == "idle") {
				return nil, "", ErrRemoteSessionActive
			}
			copy := *item
			return &copy, manager.cfg.SSHAlias, nil
		}
	}
	return nil, "", nil
}

// PreviewSession returns one current, complete remote session for the sharing
// boundary. Unlike LaunchSession it never starts or resumes an agent; callers
// may only turn the returned private record into the canonical allow-listed
// snapshot. The SSH alias and any other transport details stay in this package.
func (manager *Manager) PreviewSession(sourceID, agent, sessionID string, revision int64) (*session.Session, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if revision <= 0 || manager.cfg == nil || !manager.cfg.Enabled || manager.cfg.ID != sourceID ||
		manager.state != StateOK || !manager.complete {
		return nil, nil
	}
	for _, item := range manager.sessions {
		if item.Agent != agent || item.ID != sessionID {
			continue
		}
		switch launchBlockReason(manager.snapshot, item) {
		case LaunchBlockOversized:
			return nil, ErrRemoteSessionOversized
		case LaunchBlockMissingDetails:
			return nil, ErrRemoteSessionUnavailable
		}
		copy := *item
		// Returning the current revision lets sessionpreview produce its stable
		// stale-source response when the selected revision changed meanwhile.
		return &copy, nil
	}
	return nil, nil
}

// ReadFullSession returns one exact immutable complete record. It remains
// available from the last-good cache while the SSH source is offline.
func (manager *Manager) ReadFullSession(sourceID, agent, sessionID, revisionID string) (*fullsessionv1.Record, error) {
	manager.mu.Lock()
	if manager.cfg == nil || !manager.cfg.Enabled || manager.cfg.ID != sourceID || manager.snapshot == nil {
		manager.mu.Unlock()
		return nil, nil
	}
	var selected *fullsessionv1.Record
	for _, full := range manager.snapshot.FullRecords {
		if full.Record.Agent != agent || full.Record.SessionID != sessionID {
			continue
		}
		if full.Record.RevisionID != revisionID {
			manager.mu.Unlock()
			return nil, ErrRemoteRevisionNotFound
		}
		copy := full.Record
		selected = &copy
		break
	}
	manager.mu.Unlock()
	if selected == nil {
		return nil, nil
	}
	data, err := fullsessionv1.Marshal(*selected)
	if err != nil {
		return nil, ErrRemoteRecordCorrupt
	}
	copy, err := fullsessionv1.Decode(data)
	if err != nil {
		return nil, ErrRemoteRecordCorrupt
	}
	return &copy, nil
}

// ReadChange verifies the requested opaque change belongs to the exact source,
// agent, session, and revision before returning its body.
func (manager *Manager) ReadChange(sourceID, agent, sessionID, revisionID, changeID string) (*fullsessionv1.FileChange, error) {
	record, err := manager.ReadFullSession(sourceID, agent, sessionID, revisionID)
	if err != nil || record == nil {
		return nil, err
	}
	for _, edit := range record.Session.FileEdits {
		for _, change := range edit.Changes {
			if change.ID == changeID {
				copy := change
				return &copy, nil
			}
		}
	}
	return nil, ErrRemoteChangeNotFound
}

// SetupAliasMatches reports whether alias is the currently persisted remote
// target. SetupHelperForAlias repeats this check while taking its immutable
// configuration snapshot; this fast check lets the HTTP handler reject an
// unsaved settings draft before starting any lifecycle work.
func (manager *Manager) SetupAliasMatches(alias string) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.cfg != nil && manager.cfg.Enabled && manager.cfg.SSHAlias == alias
}

func (manager *Manager) Shutdown() {
	manager.mu.Lock()
	alias := ""
	if manager.cfg != nil {
		alias = manager.cfg.SSHAlias
	}
	manager.cancelLifeLocked()
	manager.refreshing = false
	manager.mu.Unlock()
	exitControlMasterBestEffort(alias)
}

func (manager *Manager) removeLocked() error {
	if manager.helperVersion != "" {
		return ErrHelperOwnershipConflict
	}
	manager.cancelLifeLocked()
	var sourceID string
	var alias string
	if manager.cfg != nil {
		sourceID = manager.cfg.ID
		alias = manager.cfg.SSHAlias
	}
	manager.cfg = nil
	manager.snapshot = nil
	manager.legacyStale = false
	manager.sessions = nil
	manager.familyStale = nil
	manager.state = StateDisabled
	manager.reason = reasonPtr(ReasonDisabled)
	manager.complete = true
	manager.errorCopy = ""
	manager.diagnostic = ""
	manager.refreshing = false
	manager.failures = 0
	manager.nextRetryAt = time.Time{}
	manager.lastSuccessAt = nil
	manager.lastCheckedAt = nil
	manager.transport = TransportSFTP
	manager.helper = nil
	manager.helperTarget = nil
	manager.helperVersion = ""
	manager.helperOwnershipCorrupt = false
	manager.helperProbe = helperProbeFallback
	manager.metrics = CollectionMetrics{}
	exitControlMasterBestEffort(alias)
	if sourceID != "" {
		return manager.cache.RemoveSource(sourceID)
	}
	return nil
}

func (manager *Manager) loadCacheLocked() error {
	v2, ok, err := manager.cache.LoadV2(manager.cfg.ID)
	if err != nil {
		return err
	}
	if ok {
		manager.snapshot = &v2
		manager.legacyStale = false
		manager.sessions = composeFromGeneration(toGeneration(v2), nullReadSource{}, nil, 0)
		manager.familyStale = staleSessions(v2)
		manager.lastSuccessAt = int64Ptr(v2.FetchedAtMs)
		manager.lastCheckedAt = int64Ptr(v2.FetchedAtMs)
		return nil
	}
	legacy, ok, err := manager.cache.Load(manager.cfg.ID)
	if err != nil {
		return err
	}
	if !ok {
		manager.snapshot = nil
		manager.legacyStale = false
		manager.sessions = nil
		manager.familyStale = nil
		return nil
	}
	// A v1 card stays visible as stale display data only: Families and
	// BaselineID stay empty so the next refresh starts from an empty
	// generation instead of reinterpreting v1 fingerprints as v2 state.
	manager.snapshot = &CachedSnapshotV2{
		Version: cacheV2Version, CoverageSinceMs: legacy.CoverageSinceMs,
		FetchedAtMs: legacy.FetchedAtMs, RoundTripMs: legacy.RoundTripMs,
		Coverage: legacy.Coverage,
	}
	manager.legacyStale = true
	manager.sessions = legacy.sessions()
	manager.familyStale = nil
	manager.lastSuccessAt = int64Ptr(legacy.FetchedAtMs)
	manager.lastCheckedAt = int64Ptr(legacy.FetchedAtMs)
	return nil
}

func (manager *Manager) startLifeLocked() {
	manager.cancelLifeLocked()
	manager.lifeCtx, manager.lifeCancel = context.WithCancel(context.Background())
}

func (manager *Manager) cancelLifeLocked() {
	if manager.lifeCancel != nil {
		manager.lifeCancel()
		manager.lifeCancel = nil
		manager.lifeCtx = nil
	}
}

func (manager *Manager) maybeStartRefreshLocked(remoteSinceMs int64, manual bool) {
	if manager.refreshing || manager.cfg == nil || !manager.cfg.Enabled || manager.lifeCtx == nil {
		return
	}
	if !manual && !manager.nextRetryAt.IsZero() && manager.now().Before(manager.nextRetryAt) {
		return
	}
	manager.kickRefreshLocked(remoteSinceMs)
}

func (manager *Manager) kickRefreshLocked(remoteSinceMs int64) {
	if manager.refreshing || manager.lifeCtx == nil || manager.cfg == nil {
		return
	}
	manager.refreshing = true
	if manager.snapshot == nil {
		manager.state = StateConnecting
		manager.reason = reasonPtr(ReasonInitialRefresh)
		manager.complete = false
	} else if manager.snapshot.CoverageSinceMs > remoteSinceMs {
		manager.state = StateConnecting
		manager.reason = reasonPtr(ReasonBroaderHistory)
		manager.complete = false
	}
	config := *manager.cfg
	baseline := snapshotOrEmpty(manager.snapshot)
	baseline.SourceID = config.ID
	var helper *helperTarget
	if manager.helperTarget != nil {
		copy := *manager.helperTarget
		helper = &copy
	}
	go manager.runRefresh(manager.lifeCtx, config, remoteSinceMs, baseline, helper)
}

func (manager *Manager) runRefresh(
	ctx context.Context,
	config settings.RemoteSettings,
	remoteSinceMs int64,
	baseline CachedSnapshotV2,
	helper *helperTarget,
) {
	var result refreshOutcome
	var err error
	if helper != nil {
		if err = manager.helperVerify(ctx, config.SSHAlias, *helper); err == nil {
			result, err = manager.helperRefresh(ctx, config.SSHAlias, remoteSinceMs, manager.now(), baseline, *helper)
		}
	} else {
		result, err = manager.refresh(ctx, config.SSHAlias, remoteSinceMs, manager.now(), baseline)
	}
	fetchedAt := manager.now().UnixMilli()
	var prepared *preparedCachedSnapshotV2
	var prepareErr error
	if err == nil && ctx.Err() == nil && result.Snapshot.RequestComplete {
		value, valueErr := manager.prepareRefreshSnapshot(config.ID, &result, fetchedAt, maxCacheV2FullRecordBytes)
		if valueErr != nil {
			prepareErr = valueErr
		} else {
			prepared = &value
		}
	}
	manager.mu.Lock()
	if !manager.refreshIsCurrentLocked(ctx, config) {
		manager.mu.Unlock()
		return
	}
	manager.lastCheckedAt = int64Ptr(fetchedAt)
	if prepareErr != nil {
		manager.refreshing = false
		manager.applyFailureLocked(ReasonLocalCacheFailed, "")
		manager.mu.Unlock()
		return
	}
	if err != nil {
		manager.refreshing = false
		diagnostic := result.Stderr
		if diagnostic == "" {
			diagnostic = sshErrorStderr(err)
		}
		reason := classifyError(err)
		if helper != nil {
			reason = classifyHelperError(err)
			manager.metrics = metricsFor(result)
			manager.transport = TransportHelper
		}
		manager.applyFailureLocked(reason, diagnostic)
		manager.mu.Unlock()
		return
	}
	if !result.Snapshot.RequestComplete {
		manager.refreshing = false
		manager.transport = transportFor(helper)
		manager.metrics = metricsFor(result)
		manager.applyFailureLocked(ReasonPartialAgentData, "")
		manager.mu.Unlock()
		return
	}
	reason := limitedResultReason(result)
	// Claim the cache writer while the configuration is still current, then do
	// the potentially large write without holding the manager publication lock.
	// Settings removal uses the same cache lock, so it cannot be overtaken by a
	// stale refresh recreating the removed source directory.
	manager.cache.mu.Lock()
	manager.mu.Unlock()
	storeErr := manager.cache.storePreparedV2Locked(config.ID, *prepared)
	manager.cache.mu.Unlock()

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !manager.refreshIsCurrentLocked(ctx, config) {
		return
	}
	manager.refreshing = false
	if storeErr != nil {
		manager.applyFailureLocked(ReasonLocalCacheFailed, "")
		return
	}
	if reason != nil {
		manager.transport = transportFor(helper)
		manager.metrics = metricsFor(result)
		manager.applyLimitedLocked(result, *prepared, *reason, fetchedAt)
		return
	}
	manager.publishSnapshotLocked(result, *prepared, fetchedAt)
	manager.failures = 0
	manager.nextRetryAt = time.Time{}
	manager.errorCopy = ""
	manager.diagnostic = ""
	manager.complete = true
	manager.state = StateOK
	manager.reason = nil
	manager.transport = transportFor(helper)
	manager.metrics = metricsFor(result)
}

func (manager *Manager) refreshIsCurrentLocked(ctx context.Context, config settings.RemoteSettings) bool {
	return manager.lifeCtx == ctx && manager.cfg != nil && manager.cfg.ID == config.ID &&
		manager.cfg.SSHAlias == config.SSHAlias && manager.cfg.Enabled &&
		!errors.Is(ctx.Err(), context.Canceled)
}

func (manager *Manager) prepareRefreshSnapshot(sourceID string, result *refreshOutcome, fetchedAt int64, fullRecordBudget int) (preparedCachedSnapshotV2, error) {
	result.Snapshot.FetchedAtMs = fetchedAt
	result.Snapshot.RoundTripMs = result.RoundTrip.Milliseconds()
	prepared, err := prepareCachedSnapshotV2(sourceID, result.Snapshot, fullRecordBudget)
	if err != nil {
		return preparedCachedSnapshotV2{}, err
	}
	if prepared.pruned && limitedResultReason(*result) == nil {
		result.Reason = reasonPtr(ReasonHistoryTruncated)
	}
	return prepared, nil
}

func metricsFor(result refreshOutcome) CollectionMetrics {
	metrics := result.Metrics
	if metrics.RoundTripMs == 0 {
		metrics.RoundTripMs = result.RoundTrip.Milliseconds()
	}
	return metrics
}

func transportFor(helper *helperTarget) Transport {
	if helper != nil {
		return TransportHelper
	}
	return TransportSFTP
}

func (manager *Manager) publishSnapshotLocked(result refreshOutcome, prepared preparedCachedSnapshotV2, fetchedAt int64) {
	snapshot := prepared.snapshot
	manager.snapshot = &snapshot
	manager.legacyStale = false
	manager.sessions = result.Sessions
	if prepared.pruned {
		manager.sessions = sessionsRetainedBySnapshot(result.Sessions, snapshot)
	}
	manager.familyStale = staleSessions(snapshot)
	manager.lastSuccessAt = int64Ptr(fetchedAt)
}

func sessionsRetainedBySnapshot(sessions []*session.Session, snapshot CachedSnapshotV2) []*session.Session {
	retained := make(map[remoteprotocol.FullRecordKey]bool)
	for _, family := range snapshot.Families {
		for _, record := range family.Facts.Sessions {
			retained[remoteprotocol.FullRecordKey{Vendor: family.Vendor, SessionID: record.ID}] = true
		}
	}
	result := make([]*session.Session, 0, len(sessions))
	for _, item := range sessions {
		if retained[remoteprotocol.FullRecordKey{Vendor: item.Agent, SessionID: item.ID}] {
			result = append(result, item)
		}
	}
	return result
}

func (manager *Manager) applyLimitedLocked(result refreshOutcome, prepared preparedCachedSnapshotV2, reason Reason, fetchedAt int64) {
	if !result.Snapshot.RequestComplete {
		// A limited but incomplete proposal can contain whole, individually valid
		// records. It still cannot replace the last complete generation.
		manager.applyFailureLocked(reason, "")
		return
	}
	manager.publishSnapshotLocked(result, prepared, fetchedAt)
	manager.failures++
	manager.nextRetryAt = manager.now().Add(retryBackoff(manager.failures))
	manager.state = StateLimited
	manager.reason = reasonPtr(reason)
	manager.complete = false
	manager.errorCopy = genericErrorCopy(reason)
	manager.diagnostic = ""
}

func (manager *Manager) applyFailureLocked(reason Reason, stderr string) {
	manager.failures++
	manager.nextRetryAt = manager.now().Add(retryBackoff(manager.failures))
	manager.reason = reasonPtr(reason)
	manager.errorCopy = genericErrorCopy(reason)
	manager.diagnostic = redactDiagnostic(stderr)
	manager.complete = false
	if manager.snapshot != nil {
		manager.state = StateStale
	} else {
		manager.state = StateError
	}
}

func (manager *Manager) sessionsLocked(remoteSinceMs int64) []IndexedSession {
	if manager.cfg == nil || !manager.cfg.Enabled {
		return nil
	}
	eligible := manager.state == StateOK && manager.complete
	globalStale := manager.state != StateOK && manager.state != StateLimited
	revisions := fullRecordRevisions(manager.snapshot)
	result := []IndexedSession{}
	for _, item := range manager.sessions {
		if remoteSinceMs > 0 && item.Status == nil && item.LastActivityTime < remoteSinceMs {
			continue
		}
		blockReason := launchBlockReason(manager.snapshot, item)
		indexed := IndexedSession{
			Key:         SessionKey{SourceID: manager.cfg.ID, Agent: item.Agent, SourceSessionID: item.ID},
			SourceLabel: manager.cfg.SSHAlias, Session: item,
			EligibleForAggregates: eligible,
			DisplayStale:          globalStale || manager.familyStale[remoteSessionKey{Agent: item.Agent, ID: item.ID}],
			Launchable:            blockReason == "",
			LaunchBlockReason:     blockReason,
			RevisionID:            revisions[remoteSessionKey{Agent: item.Agent, ID: item.ID}],
		}
		if indexed.DisplayStale {
			indexed.LastSeenStatus = item.Status
		}
		result = append(result, indexed)
	}
	return result
}

func fullRecordRevisions(snapshot *CachedSnapshotV2) map[remoteSessionKey]string {
	revisions := map[remoteSessionKey]string{}
	if snapshot == nil {
		return revisions
	}
	for _, full := range snapshot.FullRecords {
		revisions[remoteSessionKey{Agent: full.Record.Agent, ID: full.Record.SessionID}] = full.Record.RevisionID
	}
	return revisions
}

func launchBlockReason(snapshot *CachedSnapshotV2, item *session.Session) LaunchBlockReason {
	if snapshot != nil {
		for _, family := range snapshot.Families {
			if family.Vendor != item.Agent || family.StaleReason != remotefacts.StaleReasonOversizedFile {
				continue
			}
			for _, fact := range family.Facts.Sessions {
				if fact.ID == item.ID {
					return LaunchBlockOversized
				}
			}
		}
	}
	if item.WorkingDirectory != "" {
		return ""
	}
	return LaunchBlockMissingDetails
}

func (manager *Manager) healthLocked(remoteSinceMs int64) Health {
	return manager.healthLockedWithSessionCount(remoteSinceMs, len(manager.sessionsLocked(remoteSinceMs)))
}

func (manager *Manager) healthLockedWithSessionCount(remoteSinceMs int64, sessionCount int) Health {
	if manager.cfg == nil {
		return Health{State: StateDisabled, Complete: true, Reason: reasonPtr(ReasonDisabled)}
	}
	health := Health{
		SourceID: manager.cfg.ID, Label: manager.cfg.SSHAlias, State: manager.state,
		Complete: manager.complete, Reason: manager.reason, Error: manager.errorCopy,
		Refreshing:      manager.refreshing,
		LastSuccessAtMs: manager.lastSuccessAt,
		LastCheckedAtMs: manager.lastCheckedAt,
		SessionCount:    sessionCount,
		Transport:       manager.transport, Helper: manager.helper, Metrics: manager.metrics,
		HelperInstallationAvailable: manager.helperInstallationAvailable,
		HelperProbeState:            string(manager.helperProbe),
		HelperOwnershipRecorded:     manager.helperVersion != "" || manager.helperOwnershipCorrupt,
		HelperOwnershipCorrupt:      manager.helperOwnershipCorrupt,
	}
	if !manager.cfg.Enabled {
		health.State = StateDisabled
		health.Reason = reasonPtr(ReasonDisabled)
		health.Complete = true
		health.Error = ""
		return health
	}
	if manager.snapshot != nil {
		health.CoverageSinceMs = int64Ptr(manager.snapshot.CoverageSinceMs)
		health.RoundTripMs = int64Ptr(manager.snapshot.RoundTripMs)
		health.Coverage = slices.Clone(manager.snapshot.Coverage)
		health.Complete = manager.state == StateOK && manager.snapshot.CoverageSinceMs <= remoteSinceMs
	}
	if manager.state == StateConnecting && manager.snapshot != nil && manager.snapshot.CoverageSinceMs > remoteSinceMs {
		health.Reason = reasonPtr(ReasonBroaderHistory)
		health.Complete = false
	}
	return health
}

func probeSFTPWithOpen(ctx context.Context, alias string, open openFunc) (probeResult, error) {
	started := time.Now()
	connection, err := open(ctx, alias, OpenOptions{})
	if err != nil {
		return probeResult{RoundTrip: time.Since(started)}, err
	}
	closeErr := connection.Close()
	result := probeResult{RoundTrip: time.Since(started), Stderr: connection.Stderr()}
	if closeErr != nil && !benignSessionCloseErr(closeErr) {
		return result, closeErr
	}
	return result, nil
}

func refreshIncrementalWithOpen(
	ctx context.Context,
	alias string,
	since int64,
	now time.Time,
	baseline CachedSnapshotV2,
	open openFunc,
) (refreshOutcome, error) {
	started := time.Now()
	connection, err := open(ctx, alias, OpenOptions{})
	if err != nil {
		return refreshOutcome{}, err
	}
	snapshot, sessions, failures, err := collectIncremental(connection.Source(), since, now, baseline)
	stderr := connection.Stderr()
	if err != nil {
		closeErr := connection.Close()
		if closeErr != nil && !benignSessionCloseErr(closeErr) {
			err = closeErr
		}
		return refreshOutcome{Failures: failures, Stderr: stderr, RoundTrip: time.Since(started)}, err
	}
	closeErr := connection.Close()
	result := refreshOutcome{
		Snapshot: snapshot, Sessions: sessions, Failures: failures,
		Stderr: stderr, RoundTrip: time.Since(started),
	}
	if closeErr != nil && !benignSessionCloseErr(closeErr) {
		return result, closeErr
	}
	return result, nil
}

func noSupportedData(coverage []AgentCoverage) bool {
	if len(coverage) == 0 {
		return true
	}
	for _, item := range coverage {
		if item.CandidateFiles > 0 {
			return false
		}
	}
	return true
}

func limitedResultReason(result refreshOutcome) *Reason {
	switch {
	case result.Reason != nil:
		return result.Reason
	case len(result.Failures) > 0:
		return reasonPtr(ReasonPartialAgentData)
	case slices.ContainsFunc(result.Snapshot.Coverage, func(item AgentCoverage) bool { return item.Truncated }):
		return reasonPtr(ReasonHistoryTruncated)
	case noSupportedData(result.Snapshot.Coverage):
		return reasonPtr(ReasonNoSupportedData)
	default:
		return nil
	}
}

func classifyError(err error) Reason {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ReasonRefreshTimeout
	case errors.Is(err, context.Canceled):
		return ReasonRefreshTimeout
	case errors.Is(err, fs.ErrPermission), errors.Is(err, ErrPathDenied):
		return ReasonPermissionDenied
	case errors.Is(err, ErrFileLimit), errors.Is(err, ErrTotalLimit),
		errors.Is(err, ErrEntryLimit), errors.Is(err, ErrDepthLimit):
		return ReasonHistoryTruncated
	case errors.Is(err, ErrSymlink):
		return ReasonInvalidData
	case errors.Is(err, vendors.ErrInvalidData):
		return ReasonInvalidData
	}
	message := strings.ToLower(err.Error() + " " + sshErrorStderr(err))
	if strings.Contains(message, "host key verification failed") ||
		strings.Contains(message, "remote host identification has changed") ||
		strings.Contains(message, "no host key is known") {
		return ReasonHostKey
	}
	if strings.Contains(message, "permission denied (publickey") ||
		strings.Contains(message, "permission denied, please try again") ||
		strings.Contains(message, "too many authentication failures") {
		return ReasonAuthentication
	}
	if strings.Contains(message, "i/o timeout") || strings.Contains(message, "deadline exceeded") {
		return ReasonRefreshTimeout
	}
	if strings.Contains(message, "subsystem") || strings.Contains(message, "sftp") {
		return ReasonSFTPUnavailable
	}
	if strings.Contains(message, "broken pipe") || strings.Contains(message, "connection reset") ||
		strings.Contains(message, "connection refused") || strings.Contains(message, "unexpected eof") {
		return ReasonConnectionFailed
	}
	if strings.Contains(message, "json") ||
		strings.Contains(message, "unmarshal") ||
		strings.Contains(message, "decode") {
		return ReasonInvalidData
	}
	return ReasonConnectionFailed
}

func retryBackoff(failures int) time.Duration {
	return RemoteRetryInterval
}
