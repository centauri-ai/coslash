package synthesis

import (
	"context"
	"errors"
	"log"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"time"

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
	id    string
	mtime int64
}

type Manager struct {
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

func (m *Manager) Lookup(id string, mtime int64) *session.SessionSynthesis {
	if m == nil || m.runner == nil {
		return nil
	}
	return m.cache.Lookup(id, mtime)
}

func (m *Manager) Ensure(s *session.Session, mtime int64) bool {
	if m == nil || m.runner == nil || mtime <= 0 || !Eligible(s) {
		return false
	}
	if m.Lookup(s.ID, mtime) != nil || m.InCooldown(s.ID, mtime) {
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

		result, err := m.runner.Run(context.Background(), input)
		if err != nil {
			m.recordFailure(id, mtime, err)
			log.Printf("synthesize session %s: %v", id, err)
			return
		}
		record := Record{
			SessionID:   id,
			Mtime:       mtime,
			Model:       runnerModel(m.runner),
			GeneratedAt: m.now().UnixMilli(),
			Synthesis:   result,
		}
		if err := m.cache.Store(id, record); err != nil {
			m.recordFailure(id, mtime, err)
			log.Printf("cache synthesis for session %s: %v", id, err)
			return
		}
		m.failures.Delete(failureKey{id: id, mtime: mtime})
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

func (m *Manager) InCooldown(id string, mtime int64) bool {
	if m == nil || m.runner == nil {
		return true
	}
	now := m.now()
	if until := m.cliMissingUntil.Load(); until > now.UnixNano() {
		return true
	}
	key := failureKey{id: id, mtime: mtime}
	value, ok := m.failures.Load(key)
	if !ok {
		return false
	}
	if now.Sub(value.(time.Time)) < m.failureCooldown {
		return true
	}
	m.failures.Delete(key)
	return false
}

func (m *Manager) Run(ctx context.Context, list func() ([]*session.Session, error)) {
	if m == nil || m.runner == nil {
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
		mtime, err := TranscriptMtime(candidate.LogPath)
		if err != nil || mtime <= 0 {
			continue
		}
		if m.Ensure(candidate, mtime) {
			initiated++
			if initiated == m.sweepLimit {
				return
			}
		}
	}
}

func (m *Manager) recordFailure(id string, mtime int64, err error) {
	now := m.now()
	m.failures.Store(failureKey{id: id, mtime: mtime}, now)
	if errors.Is(err, exec.ErrNotFound) {
		m.cliMissingUntil.Store(now.Add(m.cliMissingCooldown).UnixNano())
	}
}

func runnerModel(runner Runner) string {
	if named, ok := runner.(interface{ ModelName() string }); ok {
		return named.ModelName()
	}
	return ""
}
