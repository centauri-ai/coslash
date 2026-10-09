package syncv4

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

const chunkBytes = 8 << 20

// sync-v4/v2 manifest limits (decision P34-D8). Curated manifests include one
// parsed record per family member. 8 MiB chunks reach at most
// v4MaxArtifacts+512 chunks within the 4 GiB family limit.
const (
	v4MaxArtifacts      = 4096
	v4MaxArtifactChunks = 256
	v4MaxManifestChunks = 2 * v4MaxArtifacts
)

const consentAge = 5 * time.Minute

var ErrPaused = errors.New("v4 sync paused")
var ErrPolicyBlocked = errors.New("v4 sync blocked by Hub policy")
var ErrDeviceSyncOff = errors.New("Hub turned sync off for this device")
var ErrStaleConsent = errors.New("v4 sync consent unavailable or stale")
var ErrCommandPickedUp = errors.New("v4 command picked up")

// busyRetry is how soon a pass that stopped at the Hub's active-upload limit
// runs again. The Hub admits a new upload once an open one finalizes, which
// takes seconds, so waiting a full sync interval would stretch a large first
// sync into hours.
const busyRetry = 20 * time.Second

// Busy reports the Hub's active-upload limit (rate_limited): more uploads
// open once in-flight ones finalize.
func Busy(err error) bool {
	var problem hubclient.V4Problem
	return errors.As(err, &problem) && problem.Code == "rate_limited"
}

// DeferReason names why a pass stopped, without session content: a Hub
// problem code, a local pause or consent state, or local_error.
func DeferReason(err error) string {
	var problem hubclient.V4Problem
	switch {
	case errors.As(err, &problem):
		return "hub:" + problem.Code
	case errors.Is(err, ErrPaused):
		return "paused"
	case errors.Is(err, ErrStaleConsent):
		return "consent_unavailable"
	}
	return "local_error"
}

// NextSyncDelay is the wait before the next pass: busyRetry after the Hub's
// active-upload limit stopped a pass or while uploads are still in flight
// (finalize runs asynchronously on the Hub, and history waits for recent
// sessions to be recorded), otherwise the regular interval.
func NextSyncDelay(err error, inFlight int, interval time.Duration) time.Duration {
	if errors.Is(err, ErrPolicyBlocked) || errors.Is(err, ErrDeviceSyncOff) || errors.Is(err, hubclient.ErrNotPaired) {
		return interval
	}
	var problem hubclient.V4Problem
	if errors.As(err, &problem) && problem.Code == "device_revoked" {
		return interval
	}
	if Busy(err) || inFlight > 0 {
		return busyRetry
	}
	return interval
}

type Transport interface {
	V4Binding(context.Context) (string, error)
	V4CheckIn(context.Context, hubclient.V4Queue, int64, []hubclient.V4CommandResult, []string, []hubclient.V4LogEntry) (hubclient.V4CheckIn, error)
	V4Create(context.Context, hubclient.V4Create) (hubclient.V4Status, error)
	V4Status(context.Context, string) (hubclient.V4Status, error)
	V4PutChunk(context.Context, string, hubclient.V4Missing, io.Reader) error
	V4Confirm(context.Context, string, ...hubclient.V4Missing) (hubclient.V4Status, error)
	V4Finalize(context.Context, string) (hubclient.V4Status, error)
}

type Runner struct {
	// Version is this Local's version. A failure it parks is retried by any
	// other version, which may read the source differently.
	Version             string
	Queue               *Queue
	Backup              *sessionbackupproducer.Manager
	Hub                 Transport
	Discover            func(context.Context) ([]*session.Session, error)
	DiscoverBatches     func(context.Context, func(DiscoveryBatch) error) error
	InventoryProgress   func() (files int64, running bool)
	Conditions          func(context.Context) (metered bool, batteryPercent int, err error)
	LocalPause          func() bool
	RequireImportPlan   bool
	Command             func(context.Context, hubclient.V4Command) error
	Now                 func() time.Time
	config              hubclient.V4Config
	checkedAt           time.Time
	configVersion       int64
	retryCommands       map[string]string
	scaleEnabled        bool
	chunkWorkers        int
	newRetryCommand     bool
	scaleSupported      bool
	waitReason          string
	waitSince           time.Time
	waitMu              sync.Mutex
	lastProgressCheckIn atomic.Int64
	checkInRetryUntil   atomic.Int64
	lastReportedPhase   string
	lastDiscoveryAt     time.Time
	activeSince         time.Time
	logRejectedUntil    time.Time
}

type DiscoveryBatch struct {
	Sessions     []*session.Session
	ContentBytes map[string]int64 // keyed by agent + "\x00" + session ID
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) SyncOnce(ctx context.Context) (syncErr error) {
	if r.Queue == nil || r.Backup == nil || r.Hub == nil || r.Discover == nil {
		return errors.New("v4 sync is not configured")
	}
	if r.activeSince.IsZero() {
		r.activeSince = r.now()
	}
	r.Queue.SetActiveSince(r.activeSince)
	// Retry commands stay open across passes until their session settles.
	defer func() { syncErr = errors.Join(syncErr, r.finishRetryCommands()) }()
	binding, err := r.Hub.V4Binding(ctx)
	if err != nil {
		return err
	}
	if err := r.Queue.Rebind(binding); err != nil {
		return err
	}
	if err := r.refreshConsent(ctx); err != nil {
		return err
	}
	if r.config.ImportPlan != nil {
		if err := validateImportPlan(*r.config.ImportPlan); err != nil {
			return err
		}
	}
	if r.Queue.PolicyBlocked() {
		return ErrPolicyBlocked
	}
	if r.RequireImportPlan && !r.scaleEnabled {
		return ErrPaused
	}
	if r.newRetryCommand {
		r.newRetryCommand = false
		return ErrCommandPickedUp
	}
	if err := r.discardAbandoned(); err != nil {
		return err
	}
	if r.DiscoverBatches != nil && (r.config.ImportPlan != nil || r.scaleEnabled) {
		listedOnce, unlistedSince := false, 0
		// A large local discovery pass can take minutes. Resume already listed
		// uploads first so a fresh scan does not block visible content progress.
		var transferErr error
		if plan := r.config.ImportPlan; plan != nil && len(r.Queue.PlannedEntries(*plan, r.now())) > 0 {
			transferErr = r.runPlannedAndReport(ctx)
			if stopSync(transferErr) {
				return transferErr
			}
		}
		// The inventory refreshes every five minutes. Between refreshes, use
		// the saved queue to keep transferring instead of reparsing the same
		// source files on every sync pass.
		if !r.lastDiscoveryAt.IsZero() && r.now().Sub(r.lastDiscoveryAt) < 5*time.Minute {
			return transferErr
		}
		if err := r.DiscoverBatches(ctx, func(batch DiscoveryBatch) error {
			entries := discoveredEntries(batch.Sessions, r.Queue.InstallID(), nil)
			for i := range entries {
				entries[i].ContentBytes = batch.ContentBytes[entries[i].Selection.Agent+"\x00"+entries[i].Selection.SessionID]
			}
			if r.config.ImportPlan == nil {
				if err := r.Queue.Merge(entries, r.now()); err != nil {
					return err
				}
			} else {
				count, err := r.Queue.MergePlannedDiscovery(entries, r.now(), *r.config.ImportPlan, r.activeSince)
				if err != nil {
					return err
				}
				unlistedSince += count
			}
			if err := r.releaseParked(); err != nil {
				return err
			}
			if err := r.applyExclusions(); err != nil {
				return err
			}
			if r.config.ImportPlan == nil {
				return nil
			}
			if unlistedSince == 0 || listedOnce && unlistedSince < 50 {
				return nil
			}
			if err := r.listAll(ctx, *r.config.ImportPlan); err != nil {
				return err
			}
			listedOnce, unlistedSince = true, 0
			return nil
		}); err != nil {
			return errors.Join(transferErr, err)
		}
		r.lastDiscoveryAt = r.now()
		return errors.Join(transferErr, r.runPlannedAndReport(ctx))
	}
	if r.scaleEnabled && r.config.ImportPlan == nil {
		return r.runPlannedAndReport(ctx)
	}
	sessions, err := r.Discover(ctx)
	if err != nil {
		return err
	}
	var claudeRevisions map[string]string
	for _, item := range sessions {
		if item != nil && item.Agent == "claude" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			claudeRevisions, err = claude.LocalSourceRevisions(ctx, vendors.LocalReadSource, home)
			if err != nil {
				return err
			}
			break
		}
	}
	discovered := discoveredEntries(sessions, r.Queue.InstallID(), claudeRevisions)
	if plan := r.config.ImportPlan; plan != nil {
		if _, err := r.Queue.MergePlannedDiscovery(discovered, r.now(), *plan, r.activeSince); err != nil {
			return err
		}
	} else if err := r.Queue.Merge(discovered, r.now()); err != nil {
		return err
	}
	if err := r.releaseParked(); err != nil {
		return err
	}
	if err := r.applyExclusions(); err != nil {
		return err
	}
	if r.config.ImportPlan != nil || r.scaleEnabled {
		return r.runPlannedAndReport(ctx)
	}
	if r.scaleEnabled {
		return r.runPlannedAndReport(ctx)
	}
	if r.scaleEnabled {
		return r.Queue.SetPhase("awaiting_plan")
	}
	entries := r.Queue.Entries()
	var firstErr error
	// Publish every recent metadata row before sending its first content chunk.
	for _, entry := range entries {
		if entry.Excluded || entry.ParkedVersion != "" || !retryDue(entry, r.now()) || !entry.Recent || !pending(entry) {
			continue
		}
		if err := r.ensureCreated(ctx, &entry); err != nil {
			if stopSync(err) {
				return r.persistPolicyStop(&entry, err)
			}
			if firstErr == nil {
				firstErr = err
			}
			if err := r.recordFailure(&entry, err); err != nil {
				return err
			}
		}
	}
	for _, entry := range r.Queue.Entries() {
		if entry.Excluded || entry.ParkedVersion != "" || !retryDue(entry, r.now()) || !entry.Recent || !pending(entry) || entry.UploadID == "" {
			continue
		}
		if err := r.transfer(ctx, &entry); err != nil {
			if stopSync(err) {
				return r.persistPolicyStop(&entry, err)
			}
			if firstErr == nil {
				firstErr = err
			}
			if err := r.recordFailure(&entry, err); err != nil {
				return err
			}
		}
	}
	for _, entry := range r.Queue.Entries() {
		if entry.Recent && !entry.Excluded && pending(entry) && entry.FailureCode == "" {
			return firstErr
		}
	}
	// At the Hub's active-upload limit, stop opening history uploads for this
	// pass, but keep sending the chunks of uploads that are already open so
	// the limit can clear.
	var busyErr error
	for _, entry := range r.Queue.Entries() {
		if entry.Excluded || entry.ParkedVersion != "" || !retryDue(entry, r.now()) || entry.Recent {
			continue
		}
		if !pending(entry) {
			continue
		}
		if busyErr != nil && entry.UploadID == "" {
			continue
		}
		if err := r.ensureCreated(ctx, &entry); err != nil {
			if stopSync(err) {
				return r.persistPolicyStop(&entry, err)
			}
			if Busy(err) {
				busyErr = err
			} else if firstErr == nil {
				firstErr = err
			}
			if err := r.recordFailure(&entry, err); err != nil {
				return err
			}
			continue
		}
		if entry.UploadID == "" {
			continue
		}
		if err := r.transfer(ctx, &entry); err != nil {
			if stopSync(err) {
				return r.persistPolicyStop(&entry, err)
			}
			if firstErr == nil {
				firstErr = err
			}
			if err := r.recordFailure(&entry, err); err != nil {
				return err
			}
		}
	}
	if busyErr != nil {
		return errors.Join(busyErr, firstErr)
	}
	return firstErr
}

func (r *Runner) applyExclusions() error {
	for _, entry := range r.Queue.Entries() {
		excluded := entry.ServerLeftOut || leftOut(entry.Session, r.config.LeaveOut)
		if entry.Excluded == excluded {
			continue
		}
		entry.Excluded, entry.FailureCode = excluded, ""
		if err := r.Queue.Update(entry); err != nil {
			return err
		}
	}
	return nil
}

// discoveredEntries selects root sessions of the synced local agents. Child
// sessions travel inside their root's family bundle. A Claude revision also
// covers its sidecar files, whose changes the detail revision does not see.
func discoveredEntries(sessions []*session.Session, installID string, claudeRevisions map[string]string) []Entry {
	found := make([]Entry, 0, len(sessions))
	for _, item := range sessions {
		if item == nil || (item.Agent != "codex" && item.Agent != "claude" && item.Agent != "opencode" && item.Agent != "cursor") ||
			item.ID == "" || item.ParentSessionID != "" {
			continue
		}
		// A Cursor card is eligible only with an unambiguous IDE or CLI lane.
		if item.Agent == "cursor" && (item.Entrypoint == nil || (*item.Entrypoint != "cursor-ide" && *item.Entrypoint != "cursor-cli")) {
			continue
		}
		revision := item.DetailRevision
		if item.Agent == "claude" {
			revision += ":" + claudeRevisions[item.ID]
		}
		live := false
		if item.Status != nil {
			switch *item.Status {
			case "busy", "interactive", "waiting", "idle":
				live = true
			}
		}
		meta := sessionMetadata(item, installID)
		if live {
			meta.EndedAt = nil
		}
		found = append(found, Entry{
			Key:       localKey("local", item.Agent, item.ID),
			Selection: sessionbackupproducer.Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: item.Agent, SessionID: item.ID},
			Session:   meta, Activity: item.LastActivityTime, SourceRevision: revision, Live: live,
		})
	}
	return found
}

// agentLabel names a synced agent for fallback session titles.
func agentLabel(agent string) string {
	switch agent {
	case "claude":
		return "Claude"
	case "cursor":
		return "Cursor"
	case "opencode":
		return "OpenCode"
	default:
		return "Codex"
	}
}

func stopSync(err error) bool {
	return errors.Is(err, ErrPaused) || errors.Is(err, ErrPolicyBlocked) || errors.Is(err, ErrDeviceSyncOff) ||
		policyBlocked(err) || errors.Is(err, ErrStaleConsent) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func policyBlocked(err error) bool {
	var problem hubclient.V4Problem
	return errors.Is(err, ErrPolicyBlocked) || errors.Is(err, ErrDeviceSyncOff) ||
		(errors.As(err, &problem) && (problem.Code == "sync_paused" || problem.Code == "device_sync_off"))
}

func (r *Runner) persistPolicyStop(entry *Entry, err error) error {
	var problem hubclient.V4Problem
	if errors.As(err, &problem) && (problem.Code == "sync_paused" || problem.Code == "device_sync_off") {
		return r.recordFailure(entry, err)
	}
	return err
}

func (r *Runner) recordFailure(entry *Entry, failure error) error {
	if errors.Is(failure, sessionbackupproducer.ErrNotPrepared) {
		entry.BundleID, entry.ContentSHA256, entry.UploadID = "", "", ""
		entry.Manifest = nil
		entry.Attempt++
	}
	var problem hubclient.V4Problem
	if errors.As(failure, &problem) {
		if problem.Code == "sync_paused" || problem.Code == "device_sync_off" {
			entry.FailureCode, entry.ParkedVersion, entry.LoggedFailure = "", "", ""
			entry.BackoffAttempt, entry.RetryAt = 0, time.Time{}
			if err := r.Queue.SetPolicyBlocked(); err != nil {
				return err
			}
			if err := r.Queue.Update(*entry); err != nil {
				return err
			}
			if problem.Code == "device_sync_off" {
				return ErrDeviceSyncOff
			}
			return ErrPolicyBlocked
		}
		if problem.Code == "left_out" {
			entry.Excluded, entry.ServerLeftOut, entry.FailureCode = true, true, ""
			return r.Queue.Update(*entry)
		}
		entry.FailureCode = problem.Code
		if (problem.Code == "not_found" || problem.Code == "upload_expired") && entry.UploadID != "" {
			entry.UploadID = ""
			entry.Attempt++
		}
	} else {
		var preparation *sessionbackupproducer.PreparationError
		if errors.As(failure, &preparation) {
			if preparation.TooLarge {
				entry.FailureCode = "too_large"
			} else {
				entry.FailureCode = "unreadable_source"
			}
		} else {
			entry.FailureCode = "server_error"
		}
	}
	if repeats(failure, entry.FailureCode) {
		r.park(entry)
	} else if !Busy(failure) {
		entry.BackoffAttempt++
		entry.RetryAt = r.now().Add(retryBackoff(entry.BackoffAttempt))
	}
	return r.failed(entry, failure)
}

// failed stores an entry whose failure code was just set and, when the owner
// should see the failure, a sync log line for the next check-in. A line names
// the Hub session once one exists. The same code for the same Hub session is
// logged once until the entry syncs or the Hub asks for a retry, so a failure
// that repeats on every pass or every source change does not flood the log.
func (r *Runner) failed(entry *Entry, failure error) error {
	code := hubLogCode(entry.FailureCode, failure)
	if code == "" || !pending(*entry) {
		return r.Queue.Update(*entry)
	}
	line := LogLine{At: r.now().UTC().Truncate(time.Microsecond), Level: "error", Code: code,
		Attempts: max(1, entry.Attempt+1, entry.BackoffAttempt), Parked: entry.ParkedVersion != ""}
	if strings.HasPrefix(entry.SessionID, "ses_") {
		line.SessionID = entry.SessionID
	}
	if logged := line.Code + " " + line.SessionID; logged != entry.LoggedFailure {
		entry.LoggedFailure = logged
		return r.Queue.UpdateWithLog(*entry, line)
	}
	return r.Queue.Update(*entry)
}

// parkedCodes are Hub upload failures that repeat for the same bytes.
var parkedCodes = map[string]bool{"malformed_artifact": true, "too_large": true}

// repeats reports a failure that the same source would hit again: a
// preparation problem none of which is retryable, or a Hub rejection of the
// bytes themselves.
func repeats(failure error, code string) bool {
	var preparation *sessionbackupproducer.PreparationError
	if errors.As(failure, &preparation) {
		for _, problem := range preparation.Coverage.Problems {
			if problem.Retryable {
				return false
			}
		}
		return len(preparation.Coverage.Problems) > 0
	}
	return parkedCodes[code]
}

// park stops retrying the entry until its source changes, the Hub asks for a
// retry or Local changes version, and releases its prepared bundle.
func (r *Runner) park(entry *Entry) {
	entry.ParkedVersion = r.Version
	if entry.ParkedVersion == "" {
		entry.ParkedVersion = "unknown"
	}
	entry.BundleID, entry.ContentSHA256, entry.UploadID, entry.Manifest = "", "", "", nil
}

// releaseParked retries entries that a different Local version parked.
func (r *Runner) releaseParked() error {
	for _, entry := range r.Queue.Entries() {
		if entry.ParkedVersion == "" || r.Version != "" && entry.ParkedVersion == r.Version {
			continue
		}
		entry.ParkedVersion, entry.FailureCode = "", ""
		entry.BackoffAttempt, entry.RetryAt = 0, time.Time{}
		if err := r.Queue.Update(entry); err != nil {
			return err
		}
	}
	return nil
}

// discardAbandoned deletes prepared bundles no entry refers to any more.
func (r *Runner) discardAbandoned() error {
	var deleted []string
	for _, id := range r.Queue.Discards() {
		if err := r.Backup.Discard(id); err != nil && !errors.Is(err, sessionbackupproducer.ErrNotPrepared) {
			continue
		}
		deleted = append(deleted, id)
	}
	if len(deleted) == 0 {
		return nil
	}
	return r.Queue.ForgetDiscards(deleted)
}

func localKey(sourceID, agent, sessionID string) string {
	sum := sha256.Sum256([]byte(sourceID + "\x00" + agent + "\x00" + sessionID))
	return hex.EncodeToString(sum[:])
}

func sessionMetadata(item *session.Session, installID string) hubclient.V4Session {
	meta := hubclient.V4Session{InstallID: installID, LocalKeyHash: localKey("local", item.Agent, item.ID), Agent: item.Agent,
		Title: stringValue(item.Name), Summary: stringValue(item.Summary), Repo: stringValue(item.Repository),
		Branch: stringValue(item.Branch), CWDLabel: cwdLabel(item.WorkingDirectory)}
	if meta.Title == "" {
		meta.Title = agentLabel(item.Agent) + " session"
	}
	if item.StartedAt > 0 {
		t := time.UnixMilli(item.StartedAt).UTC()
		meta.StartedAt = &t
	}
	if item.LastActivityTime > 0 {
		t := time.UnixMilli(item.LastActivityTime).UTC()
		meta.EndedAt = &t
	}
	for _, tokens := range item.Tokens {
		meta.Tokens += int64(tokens.InputTokens + tokens.OutputTokens + tokens.CacheCreationInputTokens + tokens.CacheCreation1hInputTokens + tokens.CacheReadInputTokens)
	}
	// A source may report negative counters (a parser edge case); the Hub's
	// listing contract requires tokens >= 0, so the metadata reports none.
	if meta.Tokens < 0 {
		meta.Tokens = 0
	}
	if item.Cost != nil && *item.Cost > 0 {
		meta.CostMicroUSD = int64(math.Round(*item.Cost * 1e6))
	}
	meta.Title = truncate(meta.Title, 500)
	meta.Summary = truncate(meta.Summary, 4000)
	meta.Repo = truncate(meta.Repo, 1000)
	meta.Branch = truncate(meta.Branch, 500)
	meta.CWDLabel = truncate(meta.CWDLabel, 500)
	return meta
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	value = value[:max]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (r *Runner) refreshConsent(ctx context.Context) error {
	if until := r.checkInRetryUntil.Load(); until > r.now().UnixNano() {
		return fmt.Errorf("%w: %w", ErrStaleConsent, hubclient.V4Problem{
			Code: "rate_limited", RetryAfter: time.Duration(until - r.now().UnixNano()),
		})
	}
	wasScaleSupported := r.scaleSupported
	version, config, _ := r.Queue.Policy()
	r.configVersion, r.config = version, config
	results := r.Queue.Results()
	logLimit := maxLogBatch
	if r.scaleSupported && os.Getenv("COSLASH_SCALE_IMPORT") != "0" {
		logLimit = maxScaleLogBatch
	}
	lines, through := r.Queue.PendingLogLimit(r.now(), logLimit)
	if r.now().Before(r.logRejectedUntil) {
		lines, through = nil, 0
	}
	progress := r.Queue.Progress()
	if r.scaleSupported && os.Getenv("COSLASH_SCALE_IMPORT") != "0" {
		progress.Import = r.importProgress(ctx, r.now())
	}
	if !r.scaleSupported || os.Getenv("COSLASH_SCALE_IMPORT") == "0" {
		progress.Inventory, progress.Import = nil, nil
		results = slices.DeleteFunc(results, func(result hubclient.V4CommandResult) bool { return result.Result == "in_progress" })
		for i := range results {
			results[i].Progress = nil
		}
		for i := range lines {
			lines[i].Attempts, lines[i].Parked = 0, false
		}
	}
	result, err := r.Hub.V4CheckIn(ctx, progress, version, results, r.Queue.Agents(), lines)
	logAccepted := err == nil
	if err != nil && len(lines) > 0 && logRejected(err) {
		// Keep refused lines for a later attempt but let the rest of sync
		// proceed. Avoid resending the same bad batch at heartbeat cadence.
		result, err = r.Hub.V4CheckIn(ctx, progress, version, results, r.Queue.Agents(), nil)
		if err == nil {
			r.logRejectedUntil = r.now().Add(time.Minute)
		}
	}
	if err != nil {
		var problem hubclient.V4Problem
		if errors.As(err, &problem) && problem.RetryAfter > 0 {
			r.checkInRetryUntil.Store(r.now().Add(problem.RetryAfter).UnixNano())
		}
		r.checkedAt = time.Time{}
		return fmt.Errorf("%w: %w", ErrStaleConsent, err)
	}
	r.checkInRetryUntil.Store(0)
	r.lastProgressCheckIn.Store(r.now().UnixNano())
	if logAccepted && through > 0 {
		if err := r.Queue.AcknowledgeLog(through); err != nil {
			return err
		}
		r.logRejectedUntil = time.Time{}
	}
	if err := r.Queue.ApplyPolicyAt(result, r.now()); err != nil {
		return err
	}
	if progress.Import != nil {
		r.lastReportedPhase = progress.Import.Phase
	}
	r.scaleEnabled = os.Getenv("COSLASH_SCALE_IMPORT") != "0" && slices.Contains(result.Capabilities, "scale-import/v1")
	r.scaleSupported = slices.Contains(result.Capabilities, "scale-import/v1")
	if err := r.Queue.AcknowledgeResults(results); err != nil {
		return err
	}
	for _, sent := range results {
		if sent.Result == "in_progress" && sent.Progress != nil && sent.Progress.Stage == "picked_up" {
			if err := r.Queue.SetCommandProgress(sent.CommandID, hubclient.V4CommandProgress{Stage: "preparing"}); err != nil {
				return err
			}
		}
	}
	r.configVersion, r.config, _ = r.Queue.Policy()
	r.checkedAt = r.now()
	if result.UpdateRequired {
		return hubclient.V4Problem{Code: "client_update_required"}
	}
	for _, command := range result.Commands {
		if command.ID == "" || len(command.ID) > 64 {
			continue
		}
		started, err := r.Queue.StartCommand(command.ID)
		if err != nil {
			return err
		}
		if !started {
			continue
		}
		if err := r.Queue.SetCommandProgress(command.ID, hubclient.V4CommandProgress{Stage: "picked_up"}); err != nil {
			return err
		}
		outcome := hubclient.V4CommandResult{CommandID: command.ID, Result: "done"}
		if command.Type == "retry" && r.pausedLocallyOrByHub() {
			outcome.Result, outcome.Error = "failed", "sync_paused"
		} else if command.Type == "retry" {
			var payload struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(command.Payload, &payload) != nil || !r.commandSessionAllowed(payload.SessionID) {
				outcome.Result, outcome.Error = "failed", "session_unavailable_or_left_out"
			} else if ok, err := r.Queue.RetrySession(payload.SessionID); err != nil {
				return err
			} else if !ok {
				outcome.Result, outcome.Error = "failed", "session_unavailable_or_left_out"
			} else {
				if r.retryCommands == nil {
					r.retryCommands = make(map[string]string)
				}
				r.retryCommands[command.ID] = payload.SessionID
				r.Queue.HoldCommand(command.ID)
				r.newRetryCommand = true
				continue
			}
		} else if command.Type == "prioritize" && r.scaleEnabled {
			var payload struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(command.Payload, &payload) != nil || !r.commandSessionAllowed(payload.SessionID) {
				outcome.Result, outcome.Error = "failed", "session_unavailable_or_left_out"
			} else if ok, err := r.Queue.Prioritize(payload.SessionID); err != nil {
				return err
			} else if !ok {
				outcome.Result, outcome.Error = "failed", "session_unavailable_or_left_out"
			}
		} else if command.Type == "launch" {
			var payload struct {
				SessionID string `json:"sessionId"`
				Mode      string `json:"mode"`
			}
			if json.Unmarshal(command.Payload, &payload) != nil || payload.Mode != "resume" || !r.commandSessionAllowed(payload.SessionID) {
				outcome.Result, outcome.Error = "failed", "session_unavailable_or_left_out"
			} else if r.Command == nil {
				outcome.Result, outcome.Error = "failed", "command_unavailable"
			} else if err := r.runCommand(ctx, command); err != nil {
				outcome.Result, outcome.Error = "failed", commandError(err)
			}
		} else if r.Command == nil {
			outcome.Result, outcome.Error = "failed", "command_unavailable"
		} else if err := r.runCommand(ctx, command); err != nil {
			outcome.Result, outcome.Error = "failed", commandError(err)
		}
		if err := r.Queue.FinishCommand(outcome); err != nil {
			return err
		}
	}
	if !wasScaleSupported && r.scaleSupported && os.Getenv("COSLASH_SCALE_IMPORT") != "0" {
		return r.refreshConsent(ctx)
	}
	return r.allowed(ctx, hubclient.V4Session{})
}

// logRejected reports the Hub refusing a check-in as invalid.
func logRejected(err error) bool {
	var problem hubclient.V4Problem
	return errors.As(err, &problem) && (problem.Code == "invalid_query" || problem.Code == "http_400")
}

func (r *Runner) runCommand(ctx context.Context, command hubclient.V4Command) error {
	timeout := 20 * time.Second
	if command.Type == "ssh.install" {
		timeout = 2 * time.Minute
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return r.Command(commandContext, command)
}

// finishRetryCommands reports each Hub retry by its own session's outcome:
// done once the session has an accepted revision, failed once it records a
// real failure. A session still waiting (for example behind the Hub's
// active-upload limit) keeps its command open for a later pass, so a retry
// that is about to succeed is never reported as failed.
func (r *Runner) finishRetryCommands() error {
	var resultErr error
	for commandID, sessionID := range r.retryCommands {
		result, settled := r.retryOutcome(commandID, sessionID)
		if !settled {
			continue
		}
		if err := r.Queue.FinishCommand(result); err != nil {
			resultErr = errors.Join(resultErr, err)
			continue
		}
		delete(r.retryCommands, commandID)
	}
	return resultErr
}

func (r *Runner) retryOutcome(commandID, sessionID string) (hubclient.V4CommandResult, bool) {
	failed := hubclient.V4CommandResult{CommandID: commandID, Result: "failed", Error: "retry_failed"}
	for _, entry := range r.Queue.Entries() {
		if entry.SessionID != sessionID {
			continue
		}
		switch {
		case entry.RevisionID != "" && !pending(entry):
			total := manifestBytes(entry.Manifest)
			return hubclient.V4CommandResult{CommandID: commandID, Result: "done",
				Progress: &hubclient.V4CommandProgress{Stage: "landed", BytesDone: total, BytesTotal: total}}, true
		case entry.Excluded:
			return failed, true
		case entry.FailureCode != "" && entry.FailureCode != "rate_limited":
			return failed, true
		}
		return hubclient.V4CommandResult{}, false
	}
	return failed, true
}

func (r *Runner) commandSessionAllowed(id string) bool {
	if id == "" {
		return false
	}
	for _, entry := range r.Queue.Entries() {
		if entry.SessionID == id && !entry.Excluded && !leftOut(entry.Session, r.config.LeaveOut) {
			return true
		}
	}
	return false
}

func commandError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "command_cancelled"
	}
	return "command_failed"
}

func (r *Runner) pausedLocallyOrByHub() bool {
	return r.config.Paused || r.config.DeviceOff || os.Getenv("COSLASH_SYNC_PAUSED") == "1" || r.LocalPause != nil && r.LocalPause()
}

func (r *Runner) allowed(ctx context.Context, meta hubclient.V4Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.checkedAt.IsZero() || r.now().Sub(r.checkedAt) >= consentAge {
		return ErrStaleConsent
	}
	if r.pausedLocallyOrByHub() {
		return ErrPaused
	}
	if r.Conditions != nil {
		metered, battery, err := r.Conditions(ctx)
		if err != nil {
			return ErrPaused
		}
		if metered || battery >= 0 && battery < 20 {
			return ErrPaused
		}
	}
	return nil
}

func (r *Runner) ensureConsent(ctx context.Context) error {
	phase := ""
	if r.scaleEnabled && r.Queue != nil {
		phase = r.Queue.ImportSnapshot(r.now()).Phase
	}
	if r.InventoryProgress != nil && r.scaleEnabled {
		if _, running := r.InventoryProgress(); running && phase == "awaiting_plan" {
			phase = "inventory"
		}
	}
	lastProgress := r.lastProgressCheckIn.Load()
	if lastProgress == 0 {
		lastProgress = r.checkedAt.UnixNano()
	}
	progressDue := r.scaleEnabled && (activeImportPhase(phase) && r.now().Sub(time.Unix(0, lastProgress)) >= 10*time.Second || phase != r.lastReportedPhase)
	if r.checkedAt.IsZero() || r.now().Sub(r.checkedAt) >= 4*time.Minute || progressDue {
		if err := r.refreshConsent(ctx); err != nil {
			return err
		}
	}
	return r.allowed(ctx, hubclient.V4Session{})
}

func (r *Runner) entryAllowed(entry Entry) error {
	if r.Queue != nil && !r.Queue.Matches(entry) {
		return ErrPaused
	}
	if entry.Excluded || leftOut(entry.Session, r.config.LeaveOut) {
		return hubclient.V4Problem{Code: "left_out"}
	}
	if r.scaleEnabled || r.config.ImportPlan != nil {
		if r.config.ImportPlan == nil || r.Queue == nil || !r.Queue.InPlanScope(entry, *r.config.ImportPlan) {
			return ErrPaused
		}
	}
	return nil
}

func leftOut(meta hubclient.V4Session, rules []string) bool {
	for _, rule := range rules {
		if strings.HasSuffix(rule, "/*") {
			owner := strings.TrimSuffix(rule, "/*")
			repository := strings.TrimPrefix(meta.Repo, owner+"/")
			if strings.HasPrefix(meta.Repo, owner+"/") && repository != "" && !strings.Contains(repository, "/") {
				return true
			}
			continue
		}
		if rule != "" && rule == meta.Repo {
			return true
		}
		folder := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(meta.CWDLabel)), "/")
		rule = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(rule)), "/")
		if strings.HasPrefix(rule, "~/") && (folder == rule || strings.HasPrefix(folder, rule+"/")) {
			return true
		}
	}
	return false
}

func cwdLabel(workingDirectory string) string {
	if workingDirectory == "" {
		return ""
	}
	clean, err := filepath.Abs(filepath.Clean(workingDirectory))
	if err != nil {
		clean = filepath.Clean(workingDirectory)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if absoluteHome, homeErr := filepath.Abs(filepath.Clean(home)); homeErr == nil {
			if relative, relErr := filepath.Rel(absoluteHome, clean); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
				if relative == "." {
					return "~/"
				}
				return truncate("~/"+filepath.ToSlash(relative), 500)
			}
		}
	}
	label := filepath.Base(clean)
	label = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) || value == '/' || value == '\\' {
			return '_'
		}
		return value
	}, label)
	return truncate(label, 500)
}

func (r *Runner) ensureCreated(ctx context.Context, entry *Entry) error {
	if err := r.ensureConsent(ctx); err != nil {
		return err
	}
	if err := r.entryAllowed(*entry); err != nil {
		return err
	}
	if entry.UploadID != "" || entry.RevisionID != "" {
		return nil
	}
	if err := r.prepareEntry(ctx, entry); err != nil {
		return err
	}
	return r.createUpload(ctx, entry)
}

func (r *Runner) prepareEntry(ctx context.Context, entry *Entry) error {
	if entry.BundleID == "" {
		prepared, err := r.Backup.Prepare(ctx, entry.Selection)
		if err != nil {
			return err
		}
		entry.BundleID = prepared.BundleID
		if err := r.Queue.Update(*entry); err != nil {
			return err
		}
	}
	prepared, err := r.Backup.Open(entry.BundleID)
	if err != nil {
		return err
	}
	manifest := entry.Manifest
	artifacts := curatedArtifacts(prepared)
	if !matchesCuratedManifest(manifest, artifacts) {
		built, err := r.manifest(prepared)
		if err != nil {
			return err
		}
		manifest = &built
		entry.Manifest = manifest
		entry.ContentSHA256 = manifest.ContentSHA256
		entry.ContentBytes = 0
		for _, artifact := range manifest.Artifacts {
			entry.ContentBytes += artifact.Bytes
		}
		if err := r.Queue.Update(*entry); err != nil {
			return err
		}
	}
	entry.ContentSHA256 = manifest.ContentSHA256
	return nil
}

func (r *Runner) createUpload(ctx context.Context, entry *Entry) error {
	manifest := entry.Manifest
	entry.ContentSHA256 = manifest.ContentSHA256
	key := localKey(entry.Key, manifest.ContentSHA256, fmt.Sprint(entry.Attempt))
	status, err := r.Hub.V4Create(ctx, hubclient.V4Create{IdempotencyKey: key, Session: entry.Session, Manifest: *manifest})
	if err != nil {
		return err
	}
	if status.SessionID == "" {
		return errors.New("v4 create omitted session identity")
	}
	entry.SessionID = status.SessionID
	if status.State == "completed" {
		return r.complete(entry, status)
	}
	if status.State != "open" || status.UploadID == "" {
		return errors.New("v4 create did not open upload")
	}
	entry.UploadID, entry.FailureCode = status.UploadID, ""
	return r.Queue.Update(*entry)
}

func (r *Runner) manifest(prepared *sessionbackupproducer.Prepared) (hubclient.V4Manifest, error) {
	artifacts := curatedArtifacts(prepared)
	if len(artifacts) == 0 || len(artifacts) > v4MaxArtifacts {
		return hubclient.V4Manifest{}, errors.New("v4 artifact count unsupported")
	}
	reader, err := r.Backup.Reader(prepared.BundleID)
	if err != nil {
		return hubclient.V4Manifest{}, err
	}
	manifest := hubclient.V4Manifest{ProducerVersion: prepared.Manifest.Producer.Version}
	chunks := 0
	for ordinal, artifact := range artifacts {
		if artifact.ByteLength < 1 {
			return hubclient.V4Manifest{}, errors.New("v4 empty artifact unsupported")
		}
		wire := hubclient.V4Artifact{Ordinal: ordinal, Kind: artifact.Kind, Bytes: artifact.ByteLength, SHA256: artifact.SHA256}
		for offset, chunkOrdinal := int64(0), 0; offset < artifact.ByteLength; chunkOrdinal++ {
			if chunkOrdinal >= v4MaxArtifactChunks || chunks >= v4MaxManifestChunks {
				return hubclient.V4Manifest{}, errors.New("v4 chunk count unsupported")
			}
			chunks++
			size := min(int64(chunkBytes), artifact.ByteLength-offset)
			body, err := readChunk(reader, artifact.LogicalName, offset, size)
			if err != nil {
				return hubclient.V4Manifest{}, err
			}
			sum := sha256.Sum256(body)
			wire.Chunks = append(wire.Chunks, hubclient.V4Chunk{Ordinal: chunkOrdinal, Offset: offset, Bytes: size, SHA256: hex.EncodeToString(sum[:])})
			offset += size
		}
		manifest.Artifacts = append(manifest.Artifacts, wire)
	}
	body, err := json.Marshal(struct {
		ProducerVersion string                 `json:"producerVersion"`
		Artifacts       []hubclient.V4Artifact `json:"artifacts"`
	}{manifest.ProducerVersion, manifest.Artifacts})
	if err != nil {
		return hubclient.V4Manifest{}, err
	}
	sum := sha256.Sum256(body)
	manifest.ContentSHA256 = hex.EncodeToString(sum[:])
	return manifest, nil
}

func curatedArtifacts(prepared *sessionbackupproducer.Prepared) []sessionbackupv1.Artifact {
	if prepared == nil {
		return nil
	}
	artifacts := make([]sessionbackupv1.Artifact, 0, len(prepared.Manifest.Members))
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.Kind == sessionbackupv1.KindParsedSessionRecord {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts
}

func matchesCuratedManifest(manifest *hubclient.V4Manifest, artifacts []sessionbackupv1.Artifact) bool {
	if manifest == nil || len(artifacts) == 0 || len(manifest.Artifacts) != len(artifacts) {
		return false
	}
	for ordinal, artifact := range artifacts {
		wire := manifest.Artifacts[ordinal]
		if wire.Ordinal != ordinal || wire.Kind != sessionbackupv1.KindParsedSessionRecord ||
			wire.Bytes != artifact.ByteLength || wire.SHA256 != artifact.SHA256 {
			return false
		}
	}
	return true
}

func hasOnlyCuratedArtifacts(manifest *hubclient.V4Manifest) bool {
	if manifest == nil {
		return true
	}
	if len(manifest.Artifacts) == 0 {
		return false
	}
	for ordinal, artifact := range manifest.Artifacts {
		if artifact.Ordinal != ordinal || artifact.Kind != sessionbackupv1.KindParsedSessionRecord {
			return false
		}
	}
	return true
}

type uploadAborter interface {
	V4Abort(context.Context, string) error
}

func (r *Runner) discardLegacyUpload(ctx context.Context, entry *Entry) error {
	if entry.UploadID != "" {
		status, err := r.Hub.V4Status(ctx, entry.UploadID)
		if err != nil {
			var problem hubclient.V4Problem
			if !errors.As(err, &problem) || problem.Code != "not_found" && problem.Code != "upload_expired" {
				return err
			}
		} else {
			if status.SessionID != entry.SessionID {
				return errors.New("v4 status changed session identity")
			}
			switch status.State {
			case "open", "finalizing":
				aborter, ok := r.Hub.(uploadAborter)
				if !ok {
					return errors.New("v4 transport cannot abort legacy upload")
				}
				if err := aborter.V4Abort(ctx, entry.UploadID); err != nil {
					return err
				}
			case "completed", "failed", "aborted", "expired":
			default:
				return errors.New("unknown v4 upload state")
			}
		}
	}
	entry.ContentSHA256, entry.ContentBytes, entry.UploadID, entry.SessionID = "", 0, "", ""
	entry.Manifest = nil
	entry.Attempt++
	entry.BackoffAttempt, entry.RetryAt = 0, time.Time{}
	entry.FailureCode, entry.LoggedFailure = "", ""
	return r.Queue.Update(*entry)
}

func readChunk(reader *sessionbackupproducer.BundleReader, name string, offset, size int64) ([]byte, error) {
	if size < 1 || size > chunkBytes {
		return nil, errors.New("invalid v4 chunk size")
	}
	body := make([]byte, size)
	for read := 0; read < len(body); {
		n, err := reader.Read(name, offset+int64(read), body[read:])
		read += n
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrUnexpectedEOF
		}
	}
	return body, nil
}

func (r *Runner) transfer(ctx context.Context, entry *Entry) error {
	if !pending(*entry) {
		return nil
	}
	if err := r.ensureConsent(ctx); err != nil {
		return err
	}
	if err := r.entryAllowed(*entry); err != nil {
		return err
	}
	if !hasOnlyCuratedArtifacts(entry.Manifest) {
		return r.discardLegacyUpload(ctx, entry)
	}
	if entry.RevisionID != "" {
		return nil
	}
	status, err := r.Hub.V4Status(ctx, entry.UploadID)
	if err != nil {
		return err
	}
	if status.SessionID != entry.SessionID {
		return errors.New("v4 status changed session identity")
	}
	if status.State == "completed" {
		return r.complete(entry, status)
	}
	if status.State == "failed" || status.State == "aborted" || status.State == "expired" {
		entry.FailureCode, entry.UploadID, entry.Attempt = status.FailureCode, "", entry.Attempt+1
		if entry.FailureCode == "" {
			entry.FailureCode = "server_error"
		}
		if parkedCodes[entry.FailureCode] {
			r.park(entry)
		} else if entry.FailureCode != "rate_limited" {
			entry.BackoffAttempt++
			entry.RetryAt = r.now().Add(retryBackoff(entry.BackoffAttempt))
		}
		return r.failed(entry, nil)
	}
	if status.State == "finalizing" {
		if err := r.setRetryProgress(entry.SessionID, "finalizing", manifestBytes(entry.Manifest), manifestBytes(entry.Manifest)); err != nil {
			return err
		}
		return nil
	}
	if status.State != "open" {
		return errors.New("unknown v4 upload state")
	}
	prepared, err := r.Backup.Open(entry.BundleID)
	if err != nil {
		return err
	}
	artifacts := curatedArtifacts(prepared)
	manifest := entry.Manifest
	if !matchesCuratedManifest(manifest, artifacts) || manifest.ContentSHA256 != entry.ContentSHA256 {
		return errors.New("v4 spool changed during upload")
	}
	var totalBytes, missingBytes int64
	for _, artifact := range manifest.Artifacts {
		totalBytes += artifact.Bytes
	}
	for _, missing := range status.Missing {
		missingBytes += missing.Bytes
	}
	currentDone := max(0, totalBytes-missingBytes)
	if r.Queue != nil {
		r.Queue.SetCurrent(entry.Key, currentDone, totalBytes)
		defer r.Queue.SetCurrent("", 0, 0)
	}
	if err := r.setRetryProgress(entry.SessionID, "uploading", currentDone, totalBytes); err != nil {
		return err
	}
	reader, err := r.Backup.Reader(entry.BundleID)
	if err != nil {
		return err
	}
	var sent []hubclient.V4Missing
	var sentBytes int64
	flush := func() error {
		if err := r.confirmChunks(ctx, entry.UploadID, sent); err != nil {
			return err
		}
		currentDone += sentBytes
		if r.Queue != nil {
			r.Queue.SetCurrent(entry.Key, currentDone, totalBytes)
		}
		sent, sentBytes = sent[:0], 0
		return nil
	}
	for offset := 0; offset < len(status.Missing); offset += 4 {
		if err := r.ensureConsent(ctx); err != nil {
			return err
		}
		if err := r.entryAllowed(*entry); err != nil {
			return err
		}
		end := min(offset+4, len(status.Missing))
		jobs := make([]chunkJob, 0, end-offset)
		for _, missing := range status.Missing[offset:end] {
			if missing.ArtifactOrdinal < 0 || missing.ArtifactOrdinal >= len(manifest.Artifacts) {
				return errors.New("v4 missing artifact out of range")
			}
			artifact := manifest.Artifacts[missing.ArtifactOrdinal]
			if missing.ChunkOrdinal < 0 || missing.ChunkOrdinal >= len(artifact.Chunks) {
				return errors.New("v4 missing chunk out of range")
			}
			chunk := artifact.Chunks[missing.ChunkOrdinal]
			if missing.Offset != chunk.Offset || missing.Bytes != chunk.Bytes || missing.SHA256 != chunk.SHA256 {
				return errors.New("v4 missing chunk does not match spool")
			}
			jobs = append(jobs, chunkJob{missing: missing, name: artifacts[missing.ArtifactOrdinal].LogicalName})
		}
		groupSent, putErr := r.putChunkGroup(ctx, reader, entry.UploadID, jobs)
		maxCount, maxBytes := hubclient.V4MaxConfirm, int64(chunkBytes)
		if r.scaleEnabled {
			maxCount *= 4
			maxBytes *= 4
		}
		for _, item := range groupSent {
			if len(sent) == maxCount || len(sent) > 0 && sentBytes+item.Bytes > maxBytes {
				if err := flush(); err != nil {
					return err
				}
			}
			sent = append(sent, item)
			sentBytes += item.Bytes
		}
		if putErr != nil {
			_ = flush()
			return putErr
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := r.ensureConsent(ctx); err != nil {
		return err
	}
	if err := r.entryAllowed(*entry); err != nil {
		return err
	}
	status, err = r.Hub.V4Finalize(ctx, entry.UploadID)
	if err != nil {
		return err
	}
	if status.State == "completed" {
		return r.complete(entry, status)
	}
	if status.State != "finalizing" {
		return errors.New("v4 finalize was not queued")
	}
	if err := r.setRetryProgress(entry.SessionID, "finalizing", totalBytes, totalBytes); err != nil {
		return err
	}
	return nil
}

func (r *Runner) complete(entry *Entry, status hubclient.V4Status) error {
	if status.State != "completed" || status.RevisionID == "" || status.SessionID != entry.SessionID {
		return errors.New("v4 completion lacks accepted revision")
	}
	if r.Queue != nil && !r.Queue.Matches(*entry) {
		return ErrPaused
	}
	entry.SyncedActivity, entry.SyncedSourceRevision, entry.RevisionID = entry.Activity, entry.SourceRevision, status.RevisionID
	entry.UploadID, entry.FailureCode, entry.LoggedFailure = "", "", ""
	entry.BackoffAttempt, entry.RetryAt = 0, time.Time{}
	if err := r.Queue.Update(*entry); err != nil {
		return err
	}
	return r.Backup.Discard(entry.BundleID)
}
