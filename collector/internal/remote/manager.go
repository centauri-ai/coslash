package remote

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

var ErrInvalidRemoteSettings = errors.New("invalid remote settings")

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
}

type ListResult struct {
	Sessions []IndexedSession
	Health   Health
}

type refreshResult struct {
	Sessions     []*session.Session
	Coverage     []AgentCoverage
	Failures     []error
	Fingerprints map[string][]vendors.FileFingerprint
	Stderr       string
	RoundTrip    time.Duration
}

type refreshFunc func(context.Context, string, int64, time.Time) (refreshResult, error)
type openFunc func(context.Context, string, OpenOptions) (*Session, error)

type Manager struct {
	mu sync.Mutex

	cache   *Cache
	now     func() time.Time
	refresh refreshFunc
	test    refreshFunc

	cfg        *settings.RemoteSettings
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	cached          *CachedSnapshot
	sessions        []*session.Session
	state           State
	reason          *Reason
	complete        bool
	errorCopy       string
	diagnostic      string
	refreshing      bool
	lastRequestedMs int64
	failures        int
	nextRetryAt     time.Time
	lastSuccessAt   *int64
}

type Options struct {
	Cache   *Cache
	Now     func() time.Time
	Refresh refreshFunc
	Test    refreshFunc
	Open    openFunc
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
		refresh = func(ctx context.Context, alias string, since int64, now time.Time) (refreshResult, error) {
			return refreshSFTPWithOpen(ctx, alias, since, now, open)
		}
	}
	test := options.Test
	if test == nil {
		test = func(ctx context.Context, alias string, _ int64, _ time.Time) (refreshResult, error) {
			return probeSFTPWithOpen(ctx, alias, open)
		}
	}
	return &Manager{
		cache: cache, now: now, refresh: refresh, test: test,
		state: StateDisabled, complete: true,
	}
}

func (manager *Manager) ApplySettings(remote *settings.RemoteSettings) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if remote == nil {
		return manager.removeLocked()
	}
	if !settings.ValidRemoteID(remote.ID) || !settings.ValidSSHAlias(remote.SSHAlias) {
		return ErrInvalidRemoteSettings
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
	}
	copyConfig := *remote
	manager.cfg = &copyConfig
	if !remote.Enabled {
		manager.cancelLifeLocked()
		manager.refreshing = false
		manager.state = StateDisabled
		manager.reason = reasonPtr(ReasonDisabled)
		manager.complete = true
		manager.errorCopy = ""
		exitControlMasterBestEffort(remote.SSHAlias)
		return nil
	}
	restart := previous == nil || !previous.Enabled || previous.SSHAlias != remote.SSHAlias
	if restart {
		if err := manager.loadCacheLocked(); err != nil {
			return err
		}
		manager.startLifeLocked()
		manager.state = StateConnecting
		manager.reason = reasonPtr(ReasonInitialRefresh)
		manager.complete = false
		manager.failures = 0
		manager.nextRetryAt = time.Time{}
		manager.kickRefreshLocked(manager.lastRequestedMs)
	}
	return nil
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
	if manager.cached == nil || age(manager.cached.FetchedAtMs, manager.now()) >= FreshnessInterval ||
		manager.cached.CoverageSinceMs > remoteSinceMs {
		manager.maybeStartRefreshLocked(remoteSinceMs, false)
	}
	return ListResult{Sessions: manager.sessionsLocked(remoteSinceMs), Health: manager.healthLocked(remoteSinceMs)}
}

func (manager *Manager) Retry() Health {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cfg == nil || !manager.cfg.Enabled {
		return manager.healthLocked(manager.lastRequestedMs)
	}
	manager.maybeStartRefreshLocked(manager.lastRequestedMs, true)
	return manager.healthLocked(manager.lastRequestedMs)
}

func (manager *Manager) TestAlias(ctx context.Context, alias string) (Health, error) {
	if !settings.ValidSSHAlias(alias) {
		return Health{}, ErrInvalidRemoteSettings
	}
	now := manager.now()
	result, err := manager.test(ctx, alias, now.UnixMilli(), now)
	health := Health{
		Label: alias, Complete: true, Coverage: slices.Clone(result.Coverage),
		RoundTripMs: int64Ptr(result.RoundTrip.Milliseconds()),
	}
	if err != nil {
		reason := classifyError(err)
		health.State = StateError
		health.Complete = false
		health.Reason = reasonPtr(reason)
		health.Error = genericErrorCopy(reason)
		diagnostic := result.Stderr
		if diagnostic == "" {
			diagnostic = sshErrorStderr(err)
		}
		health.DiagnosticStderr = redactDiagnostic(diagnostic)
		return health, nil
	}
	health.State = StateOK
	return health, nil
}

func (manager *Manager) DiagnosticsHealth() Health {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.healthLocked(manager.lastRequestedMs)
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
	manager.cancelLifeLocked()
	var sourceID string
	var alias string
	if manager.cfg != nil {
		sourceID = manager.cfg.ID
		alias = manager.cfg.SSHAlias
	}
	manager.cfg = nil
	manager.cached = nil
	manager.sessions = nil
	manager.state = StateDisabled
	manager.reason = reasonPtr(ReasonDisabled)
	manager.complete = true
	manager.errorCopy = ""
	manager.diagnostic = ""
	manager.refreshing = false
	manager.failures = 0
	manager.nextRetryAt = time.Time{}
	manager.lastSuccessAt = nil
	exitControlMasterBestEffort(alias)
	if sourceID != "" {
		return manager.cache.RemoveSource(sourceID)
	}
	return nil
}

func (manager *Manager) loadCacheLocked() error {
	cached, ok, err := manager.cache.Load(manager.cfg.ID)
	if err != nil {
		return err
	}
	if !ok {
		manager.cached = nil
		manager.sessions = nil
		return nil
	}
	manager.cached = &cached
	manager.sessions = cached.sessions()
	manager.lastSuccessAt = int64Ptr(cached.FetchedAtMs)
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
	if manager.cached == nil {
		manager.state = StateConnecting
		manager.reason = reasonPtr(ReasonInitialRefresh)
		manager.complete = false
	} else if manager.cached.CoverageSinceMs > remoteSinceMs {
		manager.state = StateConnecting
		manager.reason = reasonPtr(ReasonBroaderHistory)
		manager.complete = false
	}
	config := *manager.cfg
	go manager.runRefresh(manager.lifeCtx, config, remoteSinceMs)
}

func (manager *Manager) runRefresh(
	ctx context.Context,
	config settings.RemoteSettings,
	remoteSinceMs int64,
) {
	result, err := manager.refresh(ctx, config.SSHAlias, remoteSinceMs, manager.now())
	fetchedAt := manager.now().UnixMilli()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.refreshing = false
	if manager.cfg == nil || manager.cfg.ID != config.ID || !manager.cfg.Enabled || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if err != nil {
		diagnostic := result.Stderr
		if diagnostic == "" {
			diagnostic = sshErrorStderr(err)
		}
		manager.applyFailureLocked(classifyError(err), diagnostic)
		return
	}
	if reason := limitedResultReason(result); reason != nil {
		manager.applyLimitedLocked(result, *reason, remoteSinceMs, fetchedAt)
		return
	}
	if !manager.publishSnapshotLocked(result, remoteSinceMs, fetchedAt) {
		return
	}
	manager.failures = 0
	manager.nextRetryAt = time.Time{}
	manager.errorCopy = ""
	manager.diagnostic = ""
	manager.complete = true
	manager.state = StateOK
	manager.reason = nil
}

func (manager *Manager) publishSnapshotLocked(
	result refreshResult,
	remoteSinceMs, fetchedAt int64,
) bool {
	cached := snapshotForCache(
		result.Sessions, slices.Clone(result.Coverage), result.Fingerprints,
		remoteSinceMs, fetchedAt, result.RoundTrip.Milliseconds(),
	)
	if err := manager.cache.Store(manager.cfg.ID, cached); err != nil {
		manager.applyFailureLocked(ReasonLocalCacheFailed, "")
		return false
	}
	manager.cached = &cached
	manager.sessions = result.Sessions
	manager.lastSuccessAt = int64Ptr(fetchedAt)
	return true
}

func (manager *Manager) applyLimitedLocked(
	result refreshResult,
	reason Reason,
	remoteSinceMs, fetchedAt int64,
) {
	if !manager.publishSnapshotLocked(result, remoteSinceMs, fetchedAt) {
		return
	}
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
	if manager.cached != nil {
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
	displayStale := manager.state != StateOK && manager.state != StateLimited
	result := []IndexedSession{}
	for _, item := range manager.sessions {
		if remoteSinceMs > 0 && item.Status == nil && item.LastActivityTime < remoteSinceMs {
			continue
		}
		indexed := IndexedSession{
			Key:         SessionKey{SourceID: manager.cfg.ID, Agent: item.Agent, SourceSessionID: item.ID},
			SourceLabel: manager.cfg.SSHAlias, Session: item,
			EligibleForAggregates: eligible, DisplayStale: displayStale,
		}
		if indexed.DisplayStale {
			indexed.LastSeenStatus = item.Status
		}
		result = append(result, indexed)
	}
	return result
}

func (manager *Manager) healthLocked(remoteSinceMs int64) Health {
	if manager.cfg == nil {
		return Health{State: StateDisabled, Complete: true, Reason: reasonPtr(ReasonDisabled)}
	}
	health := Health{
		SourceID: manager.cfg.ID, Label: manager.cfg.SSHAlias, State: manager.state,
		Complete: manager.complete, Reason: manager.reason, Error: manager.errorCopy,
		DiagnosticStderr: manager.diagnostic, Refreshing: manager.refreshing,
		LastSuccessAtMs: manager.lastSuccessAt,
	}
	if !manager.cfg.Enabled {
		health.State = StateDisabled
		health.Reason = reasonPtr(ReasonDisabled)
		health.Complete = true
		health.Error = ""
		return health
	}
	if manager.cached != nil {
		health.CoverageSinceMs = int64Ptr(manager.cached.CoverageSinceMs)
		health.RoundTripMs = int64Ptr(manager.cached.RoundTripMs)
		health.Coverage = slices.Clone(manager.cached.Coverage)
		health.Complete = manager.state == StateOK && manager.cached.CoverageSinceMs <= remoteSinceMs
	}
	if manager.state == StateConnecting && manager.cached != nil && manager.cached.CoverageSinceMs > remoteSinceMs {
		health.Reason = reasonPtr(ReasonBroaderHistory)
		health.Complete = false
	}
	return health
}

func refreshSFTP(ctx context.Context, alias string, since int64, now time.Time) (refreshResult, error) {
	return refreshSFTPWithOpen(ctx, alias, since, now, OpenSession)
}

func probeSFTPWithOpen(ctx context.Context, alias string, open openFunc) (refreshResult, error) {
	started := time.Now()
	connection, err := open(ctx, alias, OpenOptions{})
	if err != nil {
		return refreshResult{RoundTrip: time.Since(started)}, err
	}
	closeErr := connection.Close()
	result := refreshResult{RoundTrip: time.Since(started), Stderr: connection.Stderr()}
	if closeErr != nil && !benignSessionCloseErr(closeErr) {
		return result, closeErr
	}
	return result, nil
}

func refreshSFTPWithOpen(
	ctx context.Context,
	alias string,
	since int64,
	now time.Time,
	open openFunc,
) (refreshResult, error) {
	started := time.Now()
	connection, err := open(ctx, alias, OpenOptions{})
	if err != nil {
		return refreshResult{}, err
	}
	source := connection.Source()
	parseSince := max(0, since-(24*time.Hour).Milliseconds())
	collections := map[string]vendors.RemoteCollection{}
	result := refreshResult{Fingerprints: map[string][]vendors.FileFingerprint{}}

	type agentResult struct {
		agent      string
		collection vendors.RemoteCollection
		err        error
	}
	outcomes := make(chan agentResult, 2)
	go func() {
		collection, collectErr := claude.CollectRemote(source, source.Home(), parseSince, now)
		if collectErr != nil {
			collectErr = fmt.Errorf("collect Claude remote data: %w", collectErr)
		}
		outcomes <- agentResult{agent: vendors.AgentClaude, collection: collection, err: collectErr}
	}()
	go func() {
		collection, collectErr := codex.CollectRemote(source, source.Home(), parseSince)
		if collectErr != nil {
			collectErr = fmt.Errorf("collect Codex remote data: %w", collectErr)
		}
		outcomes <- agentResult{agent: vendors.AgentCodex, collection: collection, err: collectErr}
	}()

	byAgent := map[string]agentResult{}
	for range 2 {
		item := <-outcomes
		byAgent[item.agent] = item
	}
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex} {
		item := byAgent[agent]
		if item.err != nil {
			result.Failures = append(result.Failures, item.err)
			result.Coverage = append(result.Coverage, AgentCoverage{
				Agent: agent, Error: genericErrorCopy(classifyError(item.err)),
			})
			continue
		}
		collections[agent] = item.collection
		result.Fingerprints[agent] = item.collection.Fingerprints
		result.Coverage = append(result.Coverage, coverageFor(agent, item.collection))
	}
	if len(result.Failures) == 2 {
		result.Stderr = connection.Stderr()
		_ = connection.Close()
		result.RoundTrip = time.Since(started)
		return result, errors.Join(result.Failures...)
	}
	result.Sessions = collector.ListRemote(source, collections, since)
	result.Stderr = connection.Stderr()
	closeErr := connection.Close()
	result.RoundTrip = time.Since(started)
	if closeErr != nil && !benignSessionCloseErr(closeErr) {
		return result, closeErr
	}
	return result, nil
}

func coverageFor(agent string, collection vendors.RemoteCollection) AgentCoverage {
	return AgentCoverage{
		Agent: agent, CandidateFiles: collection.CandidateFiles,
		SelectedFiles: collection.SelectedFiles, Truncated: collection.Truncated,
	}
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

func limitedResultReason(result refreshResult) *Reason {
	switch {
	case len(result.Failures) > 0:
		return reasonPtr(ReasonPartialAgentData)
	case slices.ContainsFunc(result.Coverage, func(item AgentCoverage) bool { return item.Truncated }):
		return reasonPtr(ReasonHistoryTruncated)
	case noSupportedData(result.Coverage):
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
	if strings.Contains(message, "broken pipe") || strings.Contains(message, "connection reset") ||
		strings.Contains(message, "connection refused") || strings.Contains(message, "unexpected eof") {
		return ReasonConnectionFailed
	}
	if strings.Contains(message, "subsystem") || strings.Contains(message, "sftp") {
		return ReasonSFTPUnavailable
	}
	if strings.Contains(message, "json") ||
		strings.Contains(message, "unmarshal") ||
		strings.Contains(message, "decode") {
		return ReasonInvalidData
	}
	return ReasonConnectionFailed
}

func retryBackoff(failures int) time.Duration {
	if failures <= 1 {
		return InitialRetryBackoff
	}
	delay := InitialRetryBackoff << min(failures-1, 4)
	return min(delay, MaxRetryBackoff)
}
