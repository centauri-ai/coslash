package synthesis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
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
	agent    string
	id       string
	revision int64
}

type failure struct {
	at      time.Time
	message string
}

type Manager struct {
	runnerMu              sync.RWMutex
	runner                Runner
	cache                 *Cache
	accounting            *AccountingStore
	accountingUnavailable atomic.Bool
	accountingVersion     atomic.Uint64
	workContext           context.Context
	cancelWork            context.CancelFunc
	workMu                sync.Mutex
	stopped               bool
	workers               sync.WaitGroup
	slots                 chan struct{}
	inFlight              sync.Map
	failures              sync.Map
	cliMissingUntil       atomic.Int64
	sweepLimit            int
	sweepInterval         time.Duration
	failureCooldown       time.Duration
	cliMissingCooldown    time.Duration
	now                   func() time.Time
}

func NewManager(runner Runner, accounting *AccountingStore) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		runner:             runner,
		accounting:         accounting,
		workContext:        ctx,
		cancelWork:         cancel,
		cache:              NewCache(),
		slots:              make(chan struct{}, defaultConcurrency),
		sweepLimit:         defaultSweepLimit,
		sweepInterval:      defaultSweepInterval,
		failureCooldown:    defaultFailureCooldown,
		cliMissingCooldown: defaultCLIMissingCooldown,
		now:                time.Now,
	}
}

func (m *Manager) AccountingStore() *AccountingStore { return m.accounting }
func (m *Manager) AccountingUnavailable() bool {
	return m.accounting == nil || m.accountingUnavailable.Load() || m.accounting.Incomplete()
}
func (m *Manager) AccountingVersion() string {
	return strconv.FormatUint(m.accountingVersion.Load(), 10)
}

func (m *Manager) Shutdown() {
	if m == nil {
		return
	}
	m.workMu.Lock()
	m.stopped = true
	m.cancelWork()
	m.workMu.Unlock()
	m.workers.Wait()
}

func (m *Manager) Lookup(agent, id string, revision int64) *session.SessionSynthesis {
	if m == nil {
		return nil
	}
	return m.cache.Lookup(agent, id, revision)
}

func (m *Manager) LookupLatest(agent, id string) *session.SessionSynthesis {
	if m == nil {
		return nil
	}
	return m.cache.LookupLatest(agent, id)
}

func (m *Manager) Available() bool { return m != nil && m.currentRunner() != nil }

func (m *Manager) BackendUnavailable() bool {
	if !m.Available() || m.cliMissingUntil.Load() > m.now().UnixNano() {
		return true
	}
	if runner, ok := m.currentRunner().(*CLIRunner); ok {
		_, err := exec.LookPath(runner.Bin)
		return err != nil
	}
	return false
}

func (m *Manager) Running(agent, id string) bool {
	if m == nil {
		return false
	}
	_, running := m.inFlight.Load(cacheKey{agent: agent, id: id})
	return running
}

// LoadRecord returns the exact persisted synthesis record for immutable
// backup capture. A nil manager behaves like an empty cache.
func (m *Manager) LoadRecord(agent, id string) (Record, error) {
	if m == nil {
		return Record{}, os.ErrNotExist
	}
	return m.cache.LoadRecord(agent, id)
}

func (m *Manager) Ensure(s *session.Session, revision int64) bool {
	return m.ensure(s, revision, false)
}

// EnsureForShare runs synthesis on demand for a session the user chose to share.
// Background eligibility only controls automatic synthesis work.
func (m *Manager) EnsureForShare(s *session.Session, revision int64) bool {
	return m.ensure(s, revision, true)
}

func (m *Manager) ensure(s *session.Session, revision int64, requested bool) bool {
	if s != nil && s.Agent == "pi" {
		revision = Revision(s)
	}
	if m == nil || s == nil || revision <= 0 || (!requested && !Eligible(s)) {
		return false
	}
	if m.currentRunner() == nil {
		return false
	}
	if m.Lookup(s.Agent, s.ID, revision) != nil || m.InCooldown(s.Agent, s.ID, revision) {
		return false
	}
	key := cacheKey{agent: s.Agent, id: s.ID}
	if _, loaded := m.inFlight.LoadOrStore(key, struct{}{}); loaded {
		return false
	}
	input := session.Clone(s)
	agent, id := input.Agent, input.ID
	m.workMu.Lock()
	if m.stopped {
		m.workMu.Unlock()
		m.inFlight.Delete(key)
		return false
	}
	m.workers.Add(1)
	m.workMu.Unlock()
	go func() {
		defer m.workers.Done()
		defer m.inFlight.Delete(key)
		select {
		case m.slots <- struct{}{}:
		case <-m.workContext.Done():
			return
		}
		defer func() { <-m.slots }()
		runner := m.currentRunner()
		if runner == nil {
			return
		}
		vendor, model := runner.VendorName(), runner.ModelName()
		m.execute(input, revision, runner, vendor, model, agent, id)
	}()
	return true
}

func (m *Manager) accountingError(err error) {
	m.accountingUnavailable.Store(true)
	log.Printf("synthesis accounting unavailable: %v", err)
	if m.accounting != nil {
		if markerErr := m.accounting.MarkIncomplete(context.WithoutCancel(m.workContext)); markerErr != nil {
			log.Printf("mark synthesis accounting incomplete: %v", markerErr)
		}
	}
}

func (m *Manager) execute(input *session.Session, revision int64, runner Runner, vendor, model, agent, id string) {
	m.workMu.Lock()
	stopped := m.stopped
	m.workMu.Unlock()
	if stopped || m.workContext.Err() != nil {
		return
	}
	var roundID string
	if m.accounting != nil {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			m.accountingError(err)
		} else {
			roundID = hex.EncodeToString(random[:])
			if err := m.accounting.BeginRound(m.workContext, Round{ID: roundID, SourceID: "local", Agent: agent, SessionID: id, SourceRevision: revision, StartedAtMs: m.now().UnixMilli()}); err != nil {
				if m.workContext.Err() != nil {
					return
				}
				m.accountingError(err)
				roundID = ""
			}
		}
	}
	accountingFailed := false
	invoke := func(ctx context.Context, phase string, ordinal int, prompt string) (RunResult, error) {
		if err := ctx.Err(); err != nil {
			return RunResult{}, err
		}
		started := false
		startedAt := m.now().UnixMilli()
		if roundID != "" {
			if err := m.accounting.StartInvocation(ctx, roundID, ordinal, phase, vendor, model, startedAt); err == nil {
				started = true
			} else if ctx.Err() == nil {
				log.Printf("start synthesis accounting invocation: %v", err)
			}
		}
		var result RunResult
		var err error
		ran := false
		if ctx.Err() != nil {
			err = ctx.Err()
		} else {
			ran = true
			result, err = runner.Run(ctx, prompt)
		}
		if roundID != "" && !started && ran {
			if retryErr := m.accounting.StartInvocation(context.WithoutCancel(ctx), roundID, ordinal, phase, vendor, model, startedAt); retryErr == nil {
				started = true
			} else {
				accountingFailed = true
				m.accountingError(retryErr)
			}
		}
		if started {
			outcome := "success"
			if err != nil {
				outcome = "failed"
			}
			if ctx.Err() != nil {
				outcome = "interrupted"
			}
			if finishErr := m.accounting.FinishInvocation(context.WithoutCancel(ctx), roundID, ordinal, m.now().UnixMilli(), outcome, result.Usage); finishErr != nil {
				accountingFailed = true
				m.accountingUnavailable.Store(true)
				log.Printf("finish synthesis accounting invocation: %v", finishErr)
			}
		}
		return result, err
	}
	var result session.SessionSynthesis
	var err error
	if m.workContext.Err() != nil {
		err = m.workContext.Err()
	} else {
		result, err = runSynthesisWithInvocation(m.workContext, sessionWithDetailProbes(m.workContext, input), invoke)
	}
	interrupted := m.workContext.Err() != nil
	if interrupted {
		err = m.workContext.Err()
	}
	if err == nil {
		record := Record{SessionID: id, Revision: revision, Model: model, GeneratedAt: m.now().UnixMilli(), Synthesis: result}
		err = m.cache.Store(agent, id, record)
		if err == nil {
			m.failures.Delete(failureKey{agent: agent, id: id, revision: revision})
			m.cliMissingUntil.Store(0)
		}
	}
	if err != nil {
		m.recordFailure(agent, id, revision, err)
		log.Printf("synthesize session %s: failed", id)
	}
	if roundID != "" && !accountingFailed {
		outcome := "success"
		if err != nil {
			outcome = "failed"
		}
		if interrupted {
			outcome = "interrupted"
		}
		if finishErr := m.accounting.FinishRound(context.WithoutCancel(m.workContext), roundID, m.now().UnixMilli(), outcome); finishErr != nil {
			m.accountingError(finishErr)
		}
	}
	m.accountingVersion.Add(1)
}

var probeBranchDrift = session.BranchDriftContext

func sessionWithDetailProbes(ctx context.Context, s *session.Session) *session.Session {
	inputSession := *s
	if !inputSession.GitProbed {
		inputSession.Git = probeBranchDrift(ctx, inputSession.WorkingDirectory, inputSession.Branch)
		inputSession.GitProbed = true
	}
	return &inputSession
}

func runSynthesis(ctx context.Context, runner Runner, s *session.Session) (session.SessionSynthesis, error) {
	return runSynthesisWithInvocation(ctx, s, func(ctx context.Context, _ string, _ int, input string) (RunResult, error) {
		return runner.Run(ctx, input)
	})
}

func runSynthesisWithInvocation(ctx context.Context, s *session.Session, record func(context.Context, string, int, string) (RunResult, error)) (session.SessionSynthesis, error) {
	const (
		maxSourceSynthesisRuns      = 32
		maxSynthesisRuns            = 64
		minRenderedDigestEntryBytes = 14
		maxSynthesisDigestItems     = maxSourceSynthesisRuns * maxPromptBytes / minRenderedDigestEntryBytes
	)
	if s != nil && len(s.Digest) > maxSynthesisDigestItems {
		return session.SessionSynthesis{}, fmt.Errorf("synthesis digest item limit exceeded: %d entries", len(s.Digest))
	}
	inputs := BuildInputs(s)
	if len(inputs) > maxSourceSynthesisRuns {
		return session.SessionSynthesis{}, fmt.Errorf("synthesis work limit exceeded: %d source chunks", len(inputs))
	}
	runs := len(inputs)
	ordinal := 0
	invoke := func(phase, input string) (RunResult, error) {
		current := ordinal
		ordinal++
		return record(ctx, phase, current, input)
	}
	partials := make([]session.SessionSynthesis, 0, len(inputs))
	for _, input := range inputs {
		partial, err := invoke("source", input)
		if err != nil {
			return session.SessionSynthesis{}, err
		}
		partials = append(partials, partial.Synthesis)
	}
	for len(partials) > 1 {
		inputs = BuildMergeInputs(s, partials)
		if len(inputs) >= len(partials) {
			return session.SessionSynthesis{}, fmt.Errorf("synthesis merge did not reduce %d partials", len(partials))
		}
		if runs+len(inputs) > maxSynthesisRuns {
			return session.SessionSynthesis{}, fmt.Errorf("synthesis work limit exceeded: more than %d runs", maxSynthesisRuns)
		}
		runs += len(inputs)
		merged := make([]session.SessionSynthesis, 0, len(inputs))
		for _, input := range inputs {
			partial, err := invoke("merge", input)
			if err != nil {
				return session.SessionSynthesis{}, err
			}
			merged = append(merged, partial.Synthesis)
		}
		partials = merged
	}
	result := partials[0]
	result.KeyDecisions = result.KeyDecisions[:min(len(result.KeyDecisions), maxFinalKeyDecisions)]
	return result, nil
}

func (m *Manager) InCooldown(agent, id string, revision int64) bool {
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
	key := failureKey{agent: agent, id: id, revision: revision}
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

func (m *Manager) Run(ctx context.Context, list func(context.Context) ([]*session.Session, error)) {
	if m == nil {
		return
	}
	m.workMu.Lock()
	if m.stopped {
		m.workMu.Unlock()
		return
	}
	m.workers.Add(1)
	m.workMu.Unlock()
	defer m.workers.Done()
	runCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.workContext, cancel)
	defer stop()
	defer cancel()
	if runCtx.Err() != nil {
		return
	}
	m.sweep(runCtx, list)
	ticker := time.NewTicker(m.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			m.sweep(runCtx, list)
		}
	}
}

func (m *Manager) sweep(ctx context.Context, list func(context.Context) ([]*session.Session, error)) {
	if m.currentRunner() == nil {
		return
	}
	now := m.now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	sessions, err := list(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("list sessions for synthesis: %v", err)
		return
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].LastActivityTime > sessions[j].LastActivityTime
	})
	initiated := 0
	for _, candidate := range sessions {
		if ctx.Err() != nil {
			return
		}
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

func (m *Manager) recordFailure(agent, id string, revision int64, err error) {
	now := m.now()
	m.failures.Store(failureKey{agent: agent, id: id, revision: revision}, failure{at: now, message: err.Error()})
	if errors.Is(err, exec.ErrNotFound) {
		m.cliMissingUntil.Store(now.Add(m.cliMissingCooldown).UnixNano())
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

func (m *Manager) Failure(agent, id string, revision int64) string {
	if m == nil {
		return ""
	}
	key := failureKey{agent: agent, id: id, revision: revision}
	value, ok := m.failures.Load(key)
	if !ok {
		return ""
	}
	failed := value.(failure)
	if m.now().Sub(failed.at) >= m.failureCooldown {
		m.failures.Delete(key)
		return ""
	}
	return failed.message
}

func (m *Manager) currentRunner() Runner {
	m.runnerMu.RLock()
	defer m.runnerMu.RUnlock()
	return m.runner
}
