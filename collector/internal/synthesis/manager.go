package synthesis

import (
	"context"
	"errors"
	"log"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/observe"
	"github.com/centauri-ai/coslash/collector/internal/session"
)

const (
	defaultConcurrency        = 4
	defaultSweepLimit         = 20
	defaultSweepInterval      = 30 * time.Minute
	defaultFailureCooldown    = 10 * time.Minute
	defaultCLIMissingCooldown = time.Hour
)

type failureKey struct {
	id       string
	revision int64
}

type failure struct {
	at      time.Time
	message string
}

type Manager struct {
	runnerMu           sync.RWMutex
	runner             Runner
	cache              *Cache
	slots              chan struct{}
	inFlight           sync.Map
	failures           sync.Map
	cliMissingUntil    atomic.Int64
	sweepLimit         int
	sweepInterval      time.Duration
	failureCooldown    time.Duration
	cliMissingCooldown time.Duration
	now                func() time.Time
}

func NewManager(runner Runner) *Manager {
	return &Manager{
		runner:             runner,
		cache:              NewCache(),
		slots:              make(chan struct{}, defaultConcurrency),
		sweepLimit:         defaultSweepLimit,
		sweepInterval:      defaultSweepInterval,
		failureCooldown:    defaultFailureCooldown,
		cliMissingCooldown: defaultCLIMissingCooldown,
		now:                time.Now,
	}
}

func (m *Manager) Lookup(id string, revision int64) *session.SessionSynthesis {
	if m == nil {
		return nil
	}
	return m.cache.Lookup(id, revision)
}

func (m *Manager) Ensure(s *session.Session, revision int64) bool {
	if m == nil || revision <= 0 || !Eligible(s) {
		return false
	}
	if m.currentRunner() == nil {
		return false
	}
	if m.Lookup(s.ID, revision) != nil || m.InCooldown(s.ID, revision) {
		return false
	}
	if _, loaded := m.inFlight.LoadOrStore(s.ID, struct{}{}); loaded {
		return false
	}
	input := buildInputWithDetailProbes(s)
	id := s.ID
	go func() {
		defer m.inFlight.Delete(id)
		m.slots <- struct{}{}
		defer func() { <-m.slots }()

		runner := m.currentRunner()
		if runner == nil {
			return
		}
		started := time.Now()
		result, err := runner.Run(context.Background(), input)
		if err != nil {
			m.recordFailure(id, revision, err)
			observe.Operation("synthesis.run", started, "error", "reason", synthesisFailureReason(err))
			log.Printf("synthesize session %s: %v", id, err)
			return
		}
		record := Record{
			SessionID:   id,
			Revision:    revision,
			Model:       runnerModel(runner),
			GeneratedAt: m.now().UnixMilli(),
			Synthesis:   result,
		}
		if err := m.cache.Store(id, record); err != nil {
			m.recordFailure(id, revision, err)
			observe.Operation("synthesis.run", started, "error", "reason", "cache_failed")
			log.Printf("cache synthesis for session %s: %v", id, err)
			return
		}
		observe.Operation("synthesis.run", started, "ok", "backend", runnerModel(runner))
		m.failures.Delete(failureKey{id: id, revision: revision})
		m.cliMissingUntil.Store(0)
	}()
	return true
}

func buildInputWithDetailProbes(s *session.Session) string {
	inputSession := *s
	if !inputSession.GitProbed {
		inputSession.Git = session.BranchDrift(inputSession.WorkingDirectory, inputSession.Branch)
		inputSession.GitProbed = true
	}
	return BuildInput(&inputSession)
}

func (m *Manager) InCooldown(id string, revision int64) bool {
	if m == nil {
		return true
	}
	runner := m.currentRunner()
	if runner == nil {
		return true
	}
	now := m.now()
	if until := m.cliMissingUntil.Load(); until > now.UnixNano() {
		return true
	}
	key := failureKey{id: id, revision: revision}
	value, ok := m.failures.Load(key)
	if !ok {
		return false
	}
	if now.Sub(value.(failure).at) < m.failureCooldown {
		return true
	}
	m.failures.Delete(key)
	return false
}

func (m *Manager) Run(ctx context.Context, list func() ([]*session.Session, error)) {
	if m == nil {
		return
	}
	m.sweep(list)
	ticker := time.NewTicker(m.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweep(list)
		}
	}
}

func (m *Manager) sweep(list func() ([]*session.Session, error)) {
	now := m.now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	sessions, err := list()
	if err != nil {
		log.Printf("list sessions for synthesis: %v", err)
		return
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].LastActivityTime > sessions[j].LastActivityTime
	})
	initiated := 0
	for _, candidate := range sessions {
		// Sweep today's work, plus anything still live from an earlier day.
		if candidate.Status == nil && candidate.LastActivityTime < startOfToday {
			continue
		}
		if m.Ensure(candidate, candidate.LastActivityTime) {
			initiated++
			if initiated == m.sweepLimit {
				return
			}
		}
	}
}

func (m *Manager) recordFailure(id string, revision int64, err error) {
	now := m.now()
	m.failures.Store(failureKey{id: id, revision: revision}, failure{at: now, message: err.Error()})
	observe.Event("issue.synthesis.failed",
		"reason", synthesisFailureReason(err),
		"backend", runnerModel(m.currentRunner()),
	)
	if errors.Is(err, exec.ErrNotFound) {
		m.cliMissingUntil.Store(now.Add(m.cliMissingCooldown).UnixNano())
	}
}

func synthesisFailureReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "cli_missing"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not installed") || strings.Contains(message, "not on path"):
		return "cli_missing"
	case strings.Contains(message, "authentication") || strings.Contains(message, "selected model"):
		return "auth_or_model"
	case strings.Contains(message, "decode") || strings.Contains(message, "parse") || strings.Contains(message, "envelope"):
		return "parse_failed"
	case strings.Contains(message, "timeout") || strings.Contains(message, "deadline"):
		return "timeout"
	default:
		return "other"
	}
}

func (m *Manager) SetRunner(runner Runner) {
	if m == nil {
		return
	}
	m.runnerMu.Lock()
	m.runner = runner
	m.runnerMu.Unlock()
	m.cliMissingUntil.Store(0)
	m.failures.Range(func(key, _ any) bool {
		m.failures.Delete(key)
		return true
	})
}

func (m *Manager) Failure(id string, revision int64) string {
	if m == nil {
		return ""
	}
	value, ok := m.failures.Load(failureKey{id: id, revision: revision})
	if !ok {
		return ""
	}
	failed := value.(failure)
	if m.now().Sub(failed.at) >= m.failureCooldown {
		m.failures.Delete(failureKey{id: id, revision: revision})
		return ""
	}
	return failed.message
}

func (m *Manager) currentRunner() Runner {
	m.runnerMu.RLock()
	defer m.runnerMu.RUnlock()
	return m.runner
}

func runnerModel(runner Runner) string {
	if named, ok := runner.(interface{ ModelName() string }); ok {
		return named.ModelName()
	}
	return ""
}
