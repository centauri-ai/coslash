package remote

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

var ErrInvalidRemoteSettings = errors.New("invalid remote settings")

// SessionKey distinguishes local and remote sessions that share an agent ID.
type SessionKey struct {
	SourceID        string
	Agent           string
	SourceSessionID string
}

// IndexedSession is a remote session stamped with source identity and aggregate eligibility.
type IndexedSession struct {
	Key                   SessionKey
	SourceLabel           string
	Session               remoteviewv1.Session
	EligibleForAggregates bool
	DisplayStale          bool
	LastSeenStatus        *string
}

// ListResult is the immediate memory view for one remoteSince cutoff.
type ListResult struct {
	Sessions []IndexedSession
	Health   Health
}

// Manager owns one optional remote host lifecycle.
type Manager struct {
	mu sync.Mutex

	runner ProcessRunner
	cache  *Cache
	now    func() time.Time

	cfg        *settings.RemoteSettings
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	cached          *CachedSnapshot
	probe           *remoteviewv1.Probe
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

// Options configures a Manager.
type Options struct {
	Runner ProcessRunner
	Cache  *Cache
	Now    func() time.Time
}

// NewManager constructs an idle remote manager.
func NewManager(opts Options) *Manager {
	runner := opts.Runner
	if runner == nil {
		runner = NewRunner()
	}
	cache := opts.Cache
	if cache == nil {
		cache = NewCache("")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		runner: runner,
		cache:  cache,
		now:    now,
		state:  StateDisabled,
	}
}

// ApplySettings configures, disables, or removes the remote source.
func (m *Manager) ApplySettings(remote *settings.RemoteSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if remote == nil {
		return m.removeLocked()
	}
	if !settings.ValidRemoteID(remote.ID) || !settings.ValidSSHAlias(remote.SSHAlias) {
		return ErrInvalidRemoteSettings
	}

	if m.cfg != nil && m.cfg.ID != remote.ID {
		if err := m.removeLocked(); err != nil {
			return err
		}
	}

	copyCfg := *remote
	same := m.cfg != nil && m.cfg.ID == remote.ID
	wasEnabled := same && m.cfg.Enabled

	if !remote.Enabled {
		m.cfg = &copyCfg
		m.cancelLifeLocked()
		m.refreshing = false
		m.probe = nil
		m.state = StateDisabled
		m.reason = reasonPtr(ReasonDisabled)
		m.complete = true
		m.errorCopy = ""
		return nil
	}

	if !same || !wasEnabled {
		previousCfg := m.cfg
		previousCached := m.cached
		previousLastSuccess := m.lastSuccessAt
		m.cfg = &copyCfg
		if err := m.loadCacheLocked(); err != nil {
			m.cfg = previousCfg
			m.cached = previousCached
			m.lastSuccessAt = previousLastSuccess
			return err
		}
		m.probe = nil
		m.startLifeLocked()
		m.state = StateConnecting
		m.reason = reasonPtr(ReasonInitialRefresh)
		m.complete = m.cached != nil && !m.cached.View.Truncated
		m.failures = 0
		m.nextRetryAt = time.Time{}
		cutoff := m.lastRequestedMs
		m.kickRefreshLocked(cutoff, true)
		return nil
	}

	m.cfg = &copyCfg
	return nil
}

// ListView returns the current memory snapshot for cutoff R and may start a refresh.
func (m *Manager) ListView(remoteSinceMs int64) ListResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if remoteSinceMs < 0 {
		remoteSinceMs = 0
	}
	m.lastRequestedMs = remoteSinceMs
	if m.cfg == nil {
		return ListResult{Health: Health{State: StateDisabled, Complete: true, Reason: reasonPtr(ReasonDisabled)}}
	}
	if !m.cfg.Enabled {
		return ListResult{Health: m.healthLocked(remoteSinceMs)}
	}

	needRefresh := m.cached == nil ||
		age(m.cached.FetchedAtMs, m.now()) >= FreshnessInterval ||
		m.cached.View.CoverageSinceMs > remoteSinceMs

	if needRefresh {
		manual := false
		m.maybeStartRefreshLocked(remoteSinceMs, manual)
	}
	return ListResult{
		Sessions: m.sessionsLocked(remoteSinceMs),
		Health:   m.healthLocked(remoteSinceMs),
	}
}

// Retry starts an immediate refresh for the last requested cutoff.
func (m *Manager) Retry() Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return Health{State: StateDisabled, Complete: true, Reason: reasonPtr(ReasonDisabled)}
	}
	if !m.cfg.Enabled {
		h := m.healthLocked(m.lastRequestedMs)
		return h
	}
	m.maybeStartRefreshLocked(m.lastRequestedMs, true)
	return m.healthLocked(m.lastRequestedMs)
}

// TestAlias runs a synchronous probe against a draft alias without saving settings.
func (m *Manager) TestAlias(ctx context.Context, alias string) (Health, error) {
	if !settings.ValidSSHAlias(alias) {
		return Health{}, ErrInvalidRemoteSettings
	}
	result, err := m.runner.Run(ctx, alias, ProbeCommand(), nil, RunLimits{
		Deadline:  SnapshotDeadline,
		MaxStdout: ProbeStdoutCap(),
		MaxStderr: MaxStderrBytes,
	})
	health := Health{Label: alias, Complete: true}
	if err != nil && !errors.Is(err, ErrStdoutOverflow) && !errors.Is(err, ErrStderrOverflow) {
		health.State = StateError
		health.Reason = reasonPtr(ReasonConnectionFailed)
		health.Error = genericErrorCopy(ReasonConnectionFailed)
		return health, nil
	}
	overflow := result.Overflow
	if overflow == nil && err != nil {
		overflow = err
	}
	probe, _, decodeErr := extractAndDecodeProbe(result.Stdout)
	if overflow != nil || decodeErr != nil || result.ExitCode != 0 {
		failure := classifyRunFailure(result, overflow, decodeErr, true)
		health.State = failure.State
		health.Reason = reasonPtr(failure.Reason)
		health.Error = genericErrorCopy(failure.Reason)
		health.DiagnosticStderr = redactDiagnostic(string(result.Stderr))
		return health, nil
	}
	if !probeSupportsView(probe) {
		health.State = StateUpgradeRequired
		health.Reason = reasonPtr(ReasonCollectorOutdated)
		health.Error = genericErrorCopy(ReasonCollectorOutdated)
		return health, nil
	}
	health.State = StateOK
	health.CollectorVersion = probe.CollectorVersion
	health.SchemaVersion = probe.SchemaVersion
	health.Capabilities = append([]string(nil), probe.Capabilities...)
	health.LaunchableAgents = append([]string(nil), probe.LaunchableAgents...)
	health.HostOS = probe.Host.OS
	health.HostArch = probe.Host.Arch
	return health, nil
}

// RunFramedCommand runs a fixed remote command with optional stdin through the configured alias.
func (m *Manager) RunFramedCommand(ctx context.Context, remoteCommand string, stdin []byte, maxPayload int) ([]byte, RunResult, error) {
	m.mu.Lock()
	cfg := m.cfg
	life := m.lifeCtx
	m.mu.Unlock()
	if cfg == nil || !cfg.Enabled {
		return nil, RunResult{}, fmt.Errorf("remote not configured")
	}
	runCtx := ctx
	if life != nil {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(life, cancel)
		defer stop()
	}
	if maxPayload <= 0 {
		maxPayload = MaxHandoffPayload
	}
	result, err := m.runner.Run(runCtx, cfg.SSHAlias, remoteCommand, stdin, RunLimits{
		Deadline:  SnapshotDeadline,
		MaxStdout: maxPayload + MaxPrefixNoise + MaxFrameHeader,
		MaxStderr: MaxStderrBytes,
	})
	if err != nil {
		return nil, result, err
	}
	payload, _, frameErr := remoteviewv1.ExtractFrame(result.Stdout)
	if frameErr != nil {
		return nil, result, frameErr
	}
	return payload, result, nil
}

// DiagnosticsHealth returns the current health fact block.
func (m *Manager) DiagnosticsHealth() Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.healthLocked(m.lastRequestedMs)
}

// LoadSettingsSnapshot applies configured remote settings and cache for diagnostics
// without starting SSH refresh work or deleting on-disk cache.
func (m *Manager) LoadSettingsSnapshot(remote *settings.RemoteSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cancelLifeLocked()
	m.refreshing = false
	m.failures = 0
	m.nextRetryAt = time.Time{}
	m.errorCopy = ""
	m.diagnostic = ""
	m.probe = nil

	if remote == nil {
		m.cfg = nil
		m.cached = nil
		m.lastSuccessAt = nil
		m.state = StateDisabled
		m.reason = reasonPtr(ReasonDisabled)
		m.complete = true
		return nil
	}
	if !settings.ValidRemoteID(remote.ID) || !settings.ValidSSHAlias(remote.SSHAlias) {
		return ErrInvalidRemoteSettings
	}

	copyCfg := *remote
	m.cfg = &copyCfg
	if err := m.loadCacheLocked(); err != nil {
		m.cfg = nil
		m.cached = nil
		m.lastSuccessAt = nil
		return err
	}
	if !remote.Enabled {
		m.state = StateDisabled
		m.reason = reasonPtr(ReasonDisabled)
		m.complete = true
		return nil
	}
	if m.cached == nil {
		m.state = StateConnecting
		m.reason = reasonPtr(ReasonInitialRefresh)
		m.complete = false
		return nil
	}
	if m.cached.View.Truncated {
		m.state = StateLimited
		m.reason = reasonPtr(ReasonHistoryTruncated)
		m.complete = false
		return nil
	}
	m.state = StateOK
	m.reason = nil
	m.complete = true
	return nil
}

// Shutdown cancels in-flight remote work.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelLifeLocked()
	m.refreshing = false
}

func (m *Manager) removeLocked() error {
	m.cancelLifeLocked()
	m.refreshing = false
	var removeID string
	if m.cfg != nil {
		removeID = m.cfg.ID
	}
	m.cfg = nil
	m.cached = nil
	m.probe = nil
	m.state = StateDisabled
	m.reason = reasonPtr(ReasonDisabled)
	m.complete = true
	m.errorCopy = ""
	m.diagnostic = ""
	m.failures = 0
	m.nextRetryAt = time.Time{}
	m.lastSuccessAt = nil
	if removeID != "" {
		return m.cache.RemoveSource(removeID)
	}
	return nil
}

func (m *Manager) loadCacheLocked() error {
	if m.cfg == nil {
		return nil
	}
	cached, ok, err := m.cache.Load(m.cfg.ID)
	if err != nil {
		return err
	}
	if !ok {
		m.cached = nil
		return nil
	}
	copyCached := cached
	m.cached = &copyCached
	m.lastSuccessAt = int64Ptr(cached.FetchedAtMs)
	return nil
}

func (m *Manager) startLifeLocked() {
	m.cancelLifeLocked()
	m.lifeCtx, m.lifeCancel = context.WithCancel(context.Background())
}

func (m *Manager) cancelLifeLocked() {
	if m.lifeCancel != nil {
		m.lifeCancel()
		m.lifeCancel = nil
		m.lifeCtx = nil
	}
}

func (m *Manager) maybeStartRefreshLocked(remoteSinceMs int64, manual bool) {
	if m.refreshing || m.cfg == nil || !m.cfg.Enabled || m.lifeCtx == nil {
		return
	}
	if !manual && !m.nextRetryAt.IsZero() && m.now().Before(m.nextRetryAt) {
		return
	}
	m.kickRefreshLocked(remoteSinceMs, manual)
}

func (m *Manager) kickRefreshLocked(remoteSinceMs int64, manual bool) {
	if m.refreshing || m.lifeCtx == nil || m.cfg == nil {
		return
	}
	m.refreshing = true
	if m.cached == nil {
		m.state = StateConnecting
		if m.reason == nil {
			m.reason = reasonPtr(ReasonInitialRefresh)
		}
		m.complete = false
	} else if m.cached.View.CoverageSinceMs > remoteSinceMs {
		m.state = StateConnecting
		m.reason = reasonPtr(ReasonBroaderHistory)
		m.complete = false
	}
	cfg := *m.cfg
	ctx := m.lifeCtx
	go m.refresh(ctx, cfg, remoteSinceMs, manual)
}

func (m *Manager) refresh(ctx context.Context, cfg settings.RemoteSettings, remoteSinceMs int64, manual bool) {
	defer func() {
		m.mu.Lock()
		m.refreshing = false
		m.mu.Unlock()
	}()

	if m.probe == nil {
		if !m.runProbe(ctx, cfg) {
			return
		}
	}
	m.runSnapshot(ctx, cfg, remoteSinceMs, manual)
}

func (m *Manager) runProbe(ctx context.Context, cfg settings.RemoteSettings) bool {
	runCtx, cancel := midContext(ctx, SnapshotDeadline)
	defer cancel()
	result, err := m.runner.Run(runCtx, cfg.SSHAlias, ProbeCommand(), nil, RunLimits{
		Deadline:  SnapshotDeadline,
		MaxStdout: ProbeStdoutCap(),
		MaxStderr: MaxStderrBytes,
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil || m.cfg.ID != cfg.ID {
		return false
	}
	overflow := result.Overflow
	if overflow == nil && err != nil && !errors.Is(err, ErrCanceled) {
		overflow = err
	}
	if errors.Is(err, ErrCanceled) || errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	probe, _, decodeErr := extractAndDecodeProbe(result.Stdout)
	if overflow != nil || decodeErr != nil || result.ExitCode != 0 {
		failure := classifyRunFailure(result, overflow, decodeErr, true)
		m.applyFailureLocked(failure, result, true)
		return false
	}
	if !probeSupportsView(probe) {
		m.applyFailureLocked(classifiedFailure{
			State:            StateUpgradeRequired,
			Reason:           ReasonCollectorOutdated,
			ReachedCollector: true,
		}, result, true)
		return false
	}
	copyProbe := probe
	m.probe = &copyProbe
	m.diagnostic = ""
	return true
}

func (m *Manager) runSnapshot(ctx context.Context, cfg settings.RemoteSettings, remoteSinceMs int64, manual bool) {
	requestNow := m.now()
	command, err := SnapshotCommand(remoteSinceMs, requestNow.UnixMilli())
	if err != nil {
		m.mu.Lock()
		m.applyFailureLocked(classifiedFailure{State: StateError, Reason: ReasonCollectorFailed}, RunResult{}, false)
		m.mu.Unlock()
		return
	}
	runCtx, cancel := midContext(ctx, SnapshotDeadline)
	defer cancel()
	started := m.now()
	result, runErr := m.runner.Run(runCtx, cfg.SSHAlias, command, nil, RunLimits{
		Deadline:  SnapshotDeadline,
		MaxStdout: SnapshotStdoutCap(),
		MaxStderr: MaxStderrBytes,
	})
	finished := m.now()
	if result.StartedAt.IsZero() {
		result.StartedAt = started
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = finished
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil || m.cfg.ID != cfg.ID {
		return
	}
	if errors.Is(runErr, ErrCanceled) || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	overflow := result.Overflow
	if overflow == nil && runErr != nil {
		overflow = runErr
	}
	view, _, decodeErr := extractAndDecodeView(result.Stdout)
	if overflow != nil || decodeErr != nil || result.ExitCode != 0 {
		failure := classifyRunFailure(result, overflow, decodeErr, false)
		m.applyFailureLocked(failure, result, manual)
		return
	}

	offset := clockOffsetMs(view.CollectedAtMs, result.StartedAt, result.FinishedAt)
	roundTrip := result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	adjusted := adjustView(view, offset, m.now().UnixMilli())
	cached := CachedSnapshot{
		View:             adjusted,
		FetchedAtMs:      m.now().UnixMilli(),
		ClockOffsetMs:    offset,
		RoundTripMs:      roundTrip,
		CollectorVersion: adjusted.CollectorVersion,
		SchemaVersion:    adjusted.SchemaVersion,
		Capabilities:     append([]string(nil), adjusted.Capabilities...),
		LaunchableAgents: append([]string(nil), adjusted.LaunchableAgents...),
		HostOS:           adjusted.Host.OS,
		HostArch:         adjusted.Host.Arch,
	}
	if err := m.cache.Store(cfg.ID, cached); err != nil {
		m.applyFailureLocked(classifiedFailure{State: StateError, Reason: ReasonCollectorFailed}, result, manual)
		return
	}
	copyCached := cached
	m.cached = &copyCached
	m.failures = 0
	m.nextRetryAt = time.Time{}
	m.errorCopy = ""
	m.diagnostic = ""
	m.lastSuccessAt = int64Ptr(cached.FetchedAtMs)
	if adjusted.Truncated {
		m.state = StateLimited
		m.reason = reasonPtr(ReasonHistoryTruncated)
		m.complete = false
	} else {
		m.state = StateOK
		m.reason = nil
		m.complete = adjusted.CoverageSinceMs <= m.lastRequestedMs
	}
	m.probe = &remoteviewv1.Probe{
		SchemaVersion:    adjusted.SchemaVersion,
		CollectorVersion: adjusted.CollectorVersion,
		Capabilities:     append([]string(nil), adjusted.Capabilities...),
		LaunchableAgents: append([]string(nil), adjusted.LaunchableAgents...),
		HostNowMs:        adjusted.HostNowMs,
		Host:             adjusted.Host,
	}
}

func (m *Manager) applyFailureLocked(failure classifiedFailure, result RunResult, _ bool) {
	m.failures++
	m.nextRetryAt = m.now().Add(retryBackoff(m.failures))
	m.diagnostic = redactDiagnostic(string(result.Stderr))
	m.errorCopy = genericErrorCopy(failure.Reason)
	m.reason = reasonPtr(failure.Reason)
	if m.cached != nil && failure.State != StateSetupRequired && failure.State != StateUpgradeRequired {
		m.state = StateStale
		m.complete = false
		return
	}
	if m.cached != nil && failure.State == StateUpgradeRequired {
		m.state = StateUpgradeRequired
		m.complete = false
		return
	}
	m.state = failure.State
	m.complete = false
}

func (m *Manager) sessionsLocked(remoteSinceMs int64) []IndexedSession {
	if m.cfg == nil || !m.cfg.Enabled || m.cached == nil {
		return nil
	}
	nowMs := m.now().UnixMilli()
	filtered := filterSessions(m.cached.View.Sessions, remoteSinceMs, nowMs)
	eligible := m.state == StateOK && m.complete && !m.cached.View.Truncated
	out := make([]IndexedSession, 0, len(filtered))
	for _, session := range filtered {
		item := IndexedSession{
			Key: SessionKey{
				SourceID:        m.cfg.ID,
				Agent:           session.Agent,
				SourceSessionID: session.SourceSessionID,
			},
			SourceLabel:           m.cfg.SSHAlias,
			Session:               session,
			EligibleForAggregates: eligible,
			DisplayStale:          m.state == StateStale || m.state == StateLimited || !m.complete,
		}
		if item.DisplayStale {
			item.LastSeenStatus = session.Status
		}
		out = append(out, item)
	}
	return out
}

func (m *Manager) healthLocked(remoteSinceMs int64) Health {
	if m.cfg == nil {
		return Health{State: StateDisabled, Complete: true, Reason: reasonPtr(ReasonDisabled)}
	}
	health := Health{
		SourceID:         m.cfg.ID,
		Label:            m.cfg.SSHAlias,
		State:            m.state,
		Complete:         m.complete,
		Reason:           m.reason,
		Error:            m.errorCopy,
		DiagnosticStderr: m.diagnostic,
		Refreshing:       m.refreshing,
		LastSuccessAtMs:  m.lastSuccessAt,
	}
	if !m.cfg.Enabled {
		health.State = StateDisabled
		health.Reason = reasonPtr(ReasonDisabled)
		health.Complete = true
		health.Error = ""
		return health
	}
	if m.cached != nil {
		health.CoverageSinceMs = int64Ptr(m.cached.View.CoverageSinceMs)
		health.ClockOffsetMs = int64Ptr(m.cached.ClockOffsetMs)
		health.RoundTripMs = int64Ptr(m.cached.RoundTripMs)
		health.CollectorVersion = m.cached.CollectorVersion
		health.SchemaVersion = m.cached.SchemaVersion
		health.Capabilities = append([]string(nil), m.cached.Capabilities...)
		health.LaunchableAgents = append([]string(nil), m.cached.LaunchableAgents...)
		health.HostOS = m.cached.HostOS
		health.HostArch = m.cached.HostArch
		health.Complete = m.cached.View.CoverageSinceMs <= remoteSinceMs && !m.cached.View.Truncated && m.state == StateOK
	} else if m.probe != nil {
		health.CollectorVersion = m.probe.CollectorVersion
		health.SchemaVersion = m.probe.SchemaVersion
		health.Capabilities = append([]string(nil), m.probe.Capabilities...)
		health.LaunchableAgents = append([]string(nil), m.probe.LaunchableAgents...)
		health.HostOS = m.probe.Host.OS
		health.HostArch = m.probe.Host.Arch
	}
	if m.state == StateConnecting && m.cached != nil && m.cached.View.CoverageSinceMs > remoteSinceMs {
		health.Complete = false
		health.Reason = reasonPtr(ReasonBroaderHistory)
	}
	if !m.nextRetryAt.IsZero() {
		health.NextRetryAtMs = int64Ptr(m.nextRetryAt.UnixMilli())
	}
	return health
}
