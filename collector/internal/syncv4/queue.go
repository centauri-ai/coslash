package syncv4

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const recentWindow = 72 * time.Hour

type Entry struct {
	Key                  string                          `json:"key"`
	Selection            sessionbackupproducer.Selection `json:"selection"`
	Session              hubclient.V4Session             `json:"session"`
	Activity             int64                           `json:"activity"`
	SyncedActivity       int64                           `json:"syncedActivity"`
	SourceRevision       string                          `json:"sourceRevision,omitempty"`
	SyncedSourceRevision string                          `json:"syncedSourceRevision,omitempty"`
	Recent               bool                            `json:"recent"`
	CatchUpPlanVersion   int64                           `json:"catchUpPlanVersion,omitempty"`
	ChangedPlanVersion   int64                           `json:"changedPlanVersion,omitempty"`
	ContentBytes         int64                           `json:"contentBytes,omitempty"`
	Live                 bool                            `json:"live,omitempty"`
	ChangedAt            int64                           `json:"changedAt,omitempty"`
	Listed               bool                            `json:"listed,omitempty"`
	ListRejected         bool                            `json:"listRejected,omitempty"`
	Priority             bool                            `json:"priority,omitempty"`
	BundleID             string                          `json:"bundleId,omitempty"`
	ContentSHA256        string                          `json:"contentSha256,omitempty"`
	Manifest             *hubclient.V4Manifest           `json:"manifest,omitempty"`
	UploadID             string                          `json:"uploadId,omitempty"`
	SessionID            string                          `json:"sessionId,omitempty"`
	RevisionID           string                          `json:"revisionId,omitempty"`
	FailureCode          string                          `json:"failureCode,omitempty"`
	Attempt              int                             `json:"attempt,omitempty"`
	BackoffAttempt       int                             `json:"backoffAttempt,omitempty"`
	RetryAt              time.Time                       `json:"retryAt,omitempty"`
	Excluded             bool                            `json:"excluded,omitempty"`
	ServerLeftOut        bool                            `json:"serverLeftOut,omitempty"`
	// ParkedVersion is the Local version that recorded a failure that would
	// repeat for the same source. The entry is not retried until its source
	// changes, the Hub asks for a retry, or Local runs a different version.
	ParkedVersion string `json:"parkedVersion,omitempty"`
	// LoggedFailure is the Hub log code and Hub session ID last logged for
	// this entry, so a failure that repeats on every pass or every source
	// change is logged once. A completed sync or a Hub retry clears it.
	LoggedFailure string `json:"loggedFailure,omitempty"`
}

type state struct {
	Version              int                `json:"version"`
	ScaleVersion         int                `json:"scaleVersion,omitempty"`
	InstallID            string             `json:"installId"`
	Binding              string             `json:"binding,omitempty"`
	Entries              []Entry            `json:"entries"`
	ConfigVersion        int64              `json:"configVersion,omitempty"`
	Config               hubclient.V4Config `json:"config"`
	PlanStartedAt        int64              `json:"planStartedAt,omitempty"`
	CatchUpFrozenVersion int64              `json:"catchUpFrozenVersion,omitempty"`
	NextCheckInSeconds   int                `json:"nextCheckInSeconds,omitempty"`
	Phase                string             `json:"phase,omitempty"`
	RateBytesSec         float64            `json:"rateBytesSec,omitempty"`
	RateSamples          int                `json:"rateSamples,omitempty"`
	Rates                []RateSample       `json:"rates,omitempty"`
	LastProgressAt       int64              `json:"lastProgressAt,omitempty"`
	HistoryCursorAt      int64              `json:"historyCursorAt,omitempty"`
	// PolicyKnown records that Config came from a Hub check-in, including
	// the owner's version-0 default policy.
	PolicyKnown            bool            `json:"policyKnown,omitempty"`
	MinVersion             string          `json:"minVersion,omitempty"`
	RecommendedVersion     string          `json:"recommendedVersion,omitempty"`
	RecommendedDownloadURL string          `json:"recommendedDownloadUrl,omitempty"`
	UpdateRequired         bool            `json:"updateRequired,omitempty"`
	RecommendedUpdate      bool            `json:"recommendedUpdate,omitempty"`
	Commands               []commandRecord `json:"commands,omitempty"`
	// DiscardBundles are prepared bundles no entry refers to any more. The
	// runner deletes them at the start of its next pass.
	DiscardBundles []string `json:"discardBundles,omitempty"`
	// Log holds sync log lines the Hub has not acknowledged, oldest first.
	Log    []LogLine `json:"log,omitempty"`
	LogSeq int64     `json:"logSeq,omitempty"`
	// Inventory is the last stat-only inventory; it is reported once the
	// Hub has advertised scale-import/v1 in HubCapabilities.
	Inventory       *hubclient.DeviceInventory `json:"inventory,omitempty"`
	HubCapabilities []string                   `json:"hubCapabilities,omitempty"`
}

type commandRecord struct {
	ID     string                    `json:"id"`
	Result hubclient.V4CommandResult `json:"result"`
	At     time.Time                 `json:"at,omitempty"`
}

type Queue struct {
	mu                sync.Mutex
	path              string
	state             state
	currentKey        string
	currentBytesDone  int64
	currentBytesTotal int64
	// held names started commands that are still waiting on work, such as a
	// retry waiting on its session's upload. It lives in memory only, so a
	// restart reports them as execution_interrupted.
	held map[string]bool
}

func Open(root string) (*Queue, error) {
	if root == "" {
		root = filepath.Join(settings.Home(), "sync-v4")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := protectQueueDirectory(root); err != nil {
		return nil, err
	}
	q := &Queue{path: filepath.Join(root, "queue.json")}
	data, err := readQueueFile(q.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil && len(data) == 0 {
		return nil, errors.New("empty v4 queue")
	}
	if len(data) != 0 {
		if err := json.Unmarshal(data, &q.state); err != nil || q.state.Version != 1 || q.state.InstallID == "" {
			return nil, errors.New("invalid v4 queue")
		}
		for i := range q.state.Commands {
			command := &q.state.Commands[i]
			if command.Result.Result == "in_progress" {
				command.Result = hubclient.V4CommandResult{CommandID: command.ID, Result: "failed", Error: "execution_interrupted"}
			}
		}
		q.pruneCommands(time.Now())
		if err := q.save(); err != nil {
			return nil, err
		}
		return q, nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	q.state = state{Version: 1, InstallID: fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])}
	if err := q.save(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) InstallID() string { q.mu.Lock(); defer q.mu.Unlock(); return q.state.InstallID }

func (q *Queue) Rebind(binding string) error {
	if len(binding) != 64 {
		return errors.New("invalid v4 binding")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.Binding == binding {
		return nil
	}
	prior := q.state
	prior.Entries = append([]Entry(nil), q.state.Entries...)
	if q.state.Binding != "" {
		for i := range q.state.Entries {
			entry := &q.state.Entries[i]
			q.abandon(entry.BundleID)
			entry.ParkedVersion, entry.LoggedFailure = "", ""
			entry.SyncedActivity, entry.SyncedSourceRevision = 0, ""
			entry.BundleID, entry.ContentSHA256, entry.UploadID, entry.SessionID, entry.RevisionID, entry.FailureCode = "", "", "", "", "", ""
			entry.Manifest, entry.Attempt = nil, 0
			entry.BackoffAttempt, entry.RetryAt = 0, time.Time{}
		}
		q.state.ConfigVersion, q.state.PolicyKnown = 0, false
		q.state.Config = hubclient.V4Config{}
		q.state.PlanStartedAt, q.state.Phase = 0, ""
		q.state.Commands = nil
		// Unsent lines name the previous binding's sessions.
		q.state.Log = nil
		q.state.MinVersion, q.state.RecommendedVersion, q.state.RecommendedDownloadURL = "", "", ""
		q.state.UpdateRequired, q.state.RecommendedUpdate = false, false
	}
	q.state.Binding = binding
	if err := q.save(); err != nil {
		q.state = prior
		return err
	}
	return nil
}

func (q *Queue) Policy() (int64, hubclient.V4Config, string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.state.ConfigVersion, q.state.Config, q.state.MinVersion
}

func (q *Queue) ApplyPolicy(result hubclient.V4CheckIn) error {
	return q.ApplyPolicyAt(result, time.Now())
}

func (q *Queue) ApplyPolicyAt(result hubclient.V4CheckIn, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	prior := q.state
	prior.Entries = append([]Entry(nil), q.state.Entries...)
	prior.DiscardBundles = append([]string(nil), q.state.DiscardBundles...)
	if result.ConfigVersion > q.state.ConfigVersion || !q.state.PolicyKnown {
		oldPlan := q.state.Config.ImportPlan
		q.state.ConfigVersion = result.ConfigVersion
		q.state.Config = result.Config
		q.state.PolicyKnown = true
		if result.Config.ImportPlan != nil {
			q.state.ScaleVersion = 1
			if oldPlan == nil || oldPlan.Version != result.Config.ImportPlan.Version || q.state.PlanStartedAt == 0 {
				q.state.PlanStartedAt = now.UnixMilli()
				q.state.Phase = "warm_start"
				q.state.CatchUpFrozenVersion = 0
				for i := range q.state.Entries {
					q.state.Entries[i].CatchUpPlanVersion = 0
					q.state.Entries[i].ChangedPlanVersion = 0
				}
			}
		}
		if result.Config.ImportPlan == nil {
			q.state.PlanStartedAt, q.state.Phase = 0, "awaiting_plan"
			q.state.CatchUpFrozenVersion = 0
		}
		for i := range q.state.Entries {
			entry := &q.state.Entries[i]
			entry.ServerLeftOut = false
			entry.ListRejected = false
			entry.Excluded = leftOut(entry.Session, result.Config.LeaveOut)
			if entry.Excluded {
				q.exclude(entry)
			}
		}
	}
	if result.NextCheckInSeconds > 0 {
		q.state.NextCheckInSeconds = result.NextCheckInSeconds
	}
	q.state.MinVersion = result.MinVersion
	q.state.RecommendedVersion = result.RecommendedVersion
	q.state.RecommendedDownloadURL = result.RecommendedDownloadURL
	q.state.UpdateRequired = result.UpdateRequired
	q.state.RecommendedUpdate = result.RecommendedUpdate
	q.state.HubCapabilities = append([]string(nil), result.Capabilities...)
	if err := q.save(); err != nil {
		q.state = prior
		return err
	}
	return nil
}

// HubAdvertises reports whether the last check-in response listed the
// capability.
func (q *Queue) HubAdvertises(capability string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Contains(q.state.HubCapabilities, capability)
}

// SetInventory records the latest stat-only inventory for check-in.
func (q *Queue) SetInventory(inventory hubclient.DeviceInventory) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	prior := q.state.Inventory
	q.state.Inventory = &inventory
	if err := q.save(); err != nil {
		q.state.Inventory = prior
		return err
	}
	return nil
}

// Inventory returns the recorded inventory, or nil before the first scan.
func (q *Queue) Inventory() *hubclient.DeviceInventory {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.Inventory == nil {
		return nil
	}
	inventory := *q.state.Inventory
	inventory.Agents = append([]hubclient.InventoryAgent(nil), inventory.Agents...)
	return &inventory
}

type UpdatePrompt struct {
	Available   bool   `json:"available"`
	Required    bool   `json:"required"`
	Version     string `json:"version,omitempty"`
	DownloadURL string `json:"downloadUrl,omitempty"`
}

func (q *Queue) UpdatePrompt() UpdatePrompt {
	q.mu.Lock()
	defer q.mu.Unlock()
	prompt := UpdatePrompt{Available: q.state.UpdateRequired || q.state.RecommendedUpdate,
		Required: q.state.UpdateRequired, Version: q.state.RecommendedVersion}
	if prompt.Required && prompt.Version == "" {
		prompt.Version = q.state.MinVersion
	}
	if parsed, err := url.Parse(q.state.RecommendedDownloadURL); err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil {
		prompt.DownloadURL = parsed.String()
	}
	return prompt
}

func (q *Queue) Results() []hubclient.V4CommandResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	results := make([]hubclient.V4CommandResult, 0, len(q.state.Commands))
	for _, item := range q.state.Commands {
		if item.Result.Result != "" && item.Result.Result != "in_progress" && !q.held[item.ID] {
			results = append(results, item.Result)
		}
	}
	if len(results) < 100 {
		for _, item := range q.state.Commands {
			if item.Result.Result == "in_progress" {
				results = append(results, item.Result)
				if len(results) == 100 {
					break
				}
			}
		}
	}
	if len(results) > 100 {
		results = results[:100]
	}
	return results
}

func (q *Queue) AcknowledgeResults(results []hubclient.V4CommandResult) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	prior := append([]commandRecord(nil), q.state.Commands...)
	acked := make(map[string]bool, len(results))
	for _, result := range results {
		acked[result.CommandID] = true
	}
	for i := range q.state.Commands {
		if acked[q.state.Commands[i].ID] && q.state.Commands[i].Result.Result != "in_progress" {
			q.state.Commands[i].Result = hubclient.V4CommandResult{}
		}
	}
	if err := q.save(); err != nil {
		q.state.Commands = prior
		return err
	}
	return nil
}

func (q *Queue) StartCommand(id string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	prior := append([]commandRecord(nil), q.state.Commands...)
	q.pruneCommands(time.Now())
	for _, item := range q.state.Commands {
		if item.ID == id {
			return false, nil
		}
	}
	q.state.Commands = append(q.state.Commands, commandRecord{ID: id,
		Result: hubclient.V4CommandResult{CommandID: id, Result: "failed", Error: "execution_interrupted"}, At: time.Now()})
	q.pruneCommands(time.Now())
	if err := q.save(); err != nil {
		q.state.Commands = prior
		return false, err
	}
	return true, nil
}

// HoldCommand keeps a started command out of Results until FinishCommand
// records its outcome.
func (q *Queue) HoldCommand(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.held == nil {
		q.held = make(map[string]bool)
	}
	q.held[id] = true
}

func (q *Queue) FinishCommand(result hubclient.V4CommandResult) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.state.Commands {
		if q.state.Commands[i].ID == result.CommandID {
			prior := q.state.Commands[i].Result
			q.state.Commands[i].Result = result
			if err := q.save(); err != nil {
				q.state.Commands[i].Result = prior
				return err
			}
			delete(q.held, result.CommandID)
			return nil
		}
	}
	return errors.New("v4 command was not started")
}

func (q *Queue) RetrySession(serverID string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.state.Entries {
		entry := &q.state.Entries[i]
		if entry.SessionID != serverID || entry.Excluded {
			continue
		}
		prior := *entry
		q.abandon(entry.BundleID)
		entry.ParkedVersion, entry.LoggedFailure = "", ""
		entry.Priority = true
		entry.SyncedActivity = 0
		entry.SyncedSourceRevision = ""
		entry.RevisionID = ""
		entry.FailureCode = ""
		entry.BundleID, entry.ContentSHA256, entry.UploadID = "", "", ""
		entry.Manifest = nil
		entry.Attempt++
		entry.BackoffAttempt, entry.RetryAt = 0, time.Time{}
		if err := q.save(); err != nil {
			*entry = prior
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (q *Queue) Entries() []Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	entries := append([]Entry(nil), q.state.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Priority != entries[j].Priority {
			return entries[i].Priority
		}
		if entries[i].Recent != entries[j].Recent {
			return entries[i].Recent
		}
		if entries[i].Activity != entries[j].Activity {
			return entries[i].Activity > entries[j].Activity
		}
		return entries[i].Key < entries[j].Key
	})
	return entries
}

func (q *Queue) Matches(entry Entry) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, stored := range q.state.Entries {
		if stored.Key == entry.Key {
			return !stored.Excluded && stored.Activity == entry.Activity && stored.SourceRevision == entry.SourceRevision &&
				stored.Attempt == entry.Attempt && stored.BundleID == entry.BundleID && stored.UploadID == entry.UploadID
		}
	}
	return false
}

func (q *Queue) Merge(found []Entry, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	index := make(map[string]int, len(q.state.Entries))
	for i, entry := range q.state.Entries {
		index[entry.Key] = i
	}
	for _, item := range found {
		if item.Key == "" || item.Session.Agent == "" {
			continue
		}
		if i, ok := index[item.Key]; ok {
			entry := &q.state.Entries[i]
			entry.Recent = entry.Recent || item.Activity >= now.Add(-recentWindow).UnixMilli()
			wasLive := entry.Live
			if item.ContentBytes > 0 || entry.ContentBytes == 0 {
				entry.ContentBytes = item.ContentBytes
			}
			entry.Live = item.Live
			if item.Live && !wasLive && entry.ChangedAt == 0 && item.Activity >= now.Add(-2*time.Minute).UnixMilli() {
				entry.ChangedAt = now.UnixMilli()
			}
			if wasLive && !item.Live {
				entry.Session = item.Session
				entry.Listed = false
			}
			if item.Activity > entry.Activity || item.SourceRevision != "" && item.SourceRevision != entry.SourceRevision {
				if plan := q.state.Config.ImportPlan; plan != nil && q.state.PlanStartedAt > 0 && now.UnixMilli() >= q.state.PlanStartedAt {
					entry.ChangedPlanVersion = plan.Version
				}
				if item.ContentBytes <= 0 {
					entry.ContentBytes = 0
				}
				entry.ChangedAt = now.UnixMilli()
				entry.Listed = false
				entry.ListRejected = false
				entry.Activity = max(entry.Activity, item.Activity)
				entry.SourceRevision = item.SourceRevision
				entry.Session = item.Session
				q.abandon(entry.BundleID)
				entry.ParkedVersion = ""
				entry.BundleID, entry.ContentSHA256, entry.UploadID, entry.FailureCode = "", "", "", ""
				entry.Manifest, entry.RevisionID = nil, ""
				entry.Attempt = 0
				entry.BackoffAttempt, entry.RetryAt = 0, time.Time{}
			}
		} else {
			item.Recent = item.Activity >= now.Add(-recentWindow).UnixMilli()
			item.ChangedAt = now.UnixMilli()
			index[item.Key] = len(q.state.Entries)
			q.state.Entries = append(q.state.Entries, item)
		}
	}
	return q.save()
}

func (q *Queue) Update(entry Entry) error {
	return q.update(entry, nil)
}

func (q *Queue) update(entry Entry, line *LogLine) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.state.Entries {
		if q.state.Entries[i].Key == entry.Key {
			if entry.Activity < q.state.Entries[i].Activity || entry.SourceRevision != q.state.Entries[i].SourceRevision {
				return errors.New("stale v4 queue entry")
			}
			priorEntry := q.state.Entries[i]
			priorDiscards := slices.Clone(q.state.DiscardBundles)
			priorLog, priorLogSeq := slices.Clone(q.state.Log), q.state.LogSeq
			priorProgress, priorCursor := q.state.LastProgressAt, q.state.HistoryCursorAt
			if entry.Excluded && !q.state.Entries[i].Excluded {
				q.exclude(&entry)
			}
			if stored := q.state.Entries[i].BundleID; stored != entry.BundleID {
				q.abandon(stored)
			}
			q.state.Entries[i] = entry
			if entry.RevisionID != "" && !pending(entry) && (priorEntry.RevisionID != entry.RevisionID || pending(priorEntry)) {
				q.state.LastProgressAt = time.Now().UnixMilli()
				if plan := q.state.Config.ImportPlan; plan != nil && !isCatchUpEntry(entry, *plan) && entry.Activity < q.state.PlanStartedAt {
					q.state.HistoryCursorAt = entry.Activity
				}
			}
			if line != nil && !q.pendingDeviceLine(*line) {
				q.appendLog(*line)
			}
			if err := q.save(); err != nil {
				q.state.Entries[i] = priorEntry
				q.state.DiscardBundles = priorDiscards
				q.state.Log, q.state.LogSeq = priorLog, priorLogSeq
				q.state.LastProgressAt, q.state.HistoryCursorAt = priorProgress, priorCursor
				return err
			}
			return nil
		}
	}
	return errors.New("v4 queue entry missing")
}

// abandon schedules a prepared bundle that no entry uses for deletion. The
// caller holds q.mu.
func (q *Queue) abandon(bundleID string) {
	if bundleID != "" && !slices.Contains(q.state.DiscardBundles, bundleID) {
		q.state.DiscardBundles = append(q.state.DiscardBundles, bundleID)
	}
}

func (q *Queue) exclude(entry *Entry) {
	q.abandon(entry.BundleID)
	entry.BundleID, entry.ContentSHA256, entry.UploadID, entry.Manifest = "", "", "", nil
	entry.Priority = false
}

// Discards lists the abandoned bundles still to delete.
func (q *Queue) Discards() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.state.DiscardBundles)
}

// ForgetDiscards drops deleted bundles from the discard list.
func (q *Queue) ForgetDiscards(deleted []string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	prior := slices.Clone(q.state.DiscardBundles)
	q.state.DiscardBundles = slices.DeleteFunc(q.state.DiscardBundles, func(id string) bool { return slices.Contains(deleted, id) })
	if err := q.save(); err != nil {
		q.state.DiscardBundles = prior
		return err
	}
	return nil
}

// Agents lists, sorted, the agents of the sessions this install has queued.
func (q *Queue) Agents() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	seen := map[string]bool{}
	agents := []string{}
	for _, entry := range q.state.Entries {
		if agent := entry.Session.Agent; agent != "" && !seen[agent] {
			seen[agent] = true
			agents = append(agents, agent)
		}
	}
	sort.Strings(agents)
	return agents
}

// InFlight counts entries with an open or finalizing upload whose revision
// Local has not recorded yet.
func (q *Queue) InFlight() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := 0
	for _, entry := range q.state.Entries {
		if entry.UploadID != "" && entry.RevisionID == "" {
			count++
		}
	}
	return count
}

func (q *Queue) Progress() hubclient.V4Queue {
	q.mu.Lock()
	defer q.mu.Unlock()
	var progress hubclient.V4Queue
	progress.FirstSync.HistoryState = "complete"
	plan := q.state.Config.ImportPlan
	startedAt := time.Time{}
	if q.state.PlanStartedAt > 0 {
		startedAt = time.UnixMilli(q.state.PlanStartedAt)
	}
	if plan != nil && !plan.History {
		progress.FirstSync.HistoryState = "syncing"
	}
	for _, entry := range q.state.Entries {
		if plan != nil && !inScope(entry, *plan, startedAt) || plan == nil && entry.Excluded {
			continue
		}
		if plan != nil && isCatchUpEntry(entry, *plan) || plan == nil && entry.Recent {
			progress.FirstSync.RecentTotal++
			if !pending(entry) {
				progress.FirstSync.RecentDone++
			}
		} else if plan != nil && plan.History && pending(entry) || plan == nil && pending(entry) && !entry.Recent {
			progress.FirstSync.HistoryState = "syncing"
		}
		if pending(entry) {
			progress.Pending++
		}
		// rate_limited is the Hub's active-upload back-pressure, not a failure.
		if entry.FailureCode != "" && entry.FailureCode != "rate_limited" {
			progress.Failing++
		}
	}
	if plan != nil && !plan.History && q.state.CatchUpFrozenVersion == plan.Version && progress.FirstSync.RecentDone == progress.FirstSync.RecentTotal {
		progress.FirstSync.HistoryState = "off"
	}
	if q.state.Inventory != nil && hubclient.ScaleImportEnabled() && slices.Contains(q.state.HubCapabilities, hubclient.CapabilityScaleImport) {
		inventory := *q.state.Inventory
		inventory.Agents = append([]hubclient.InventoryAgent{}, inventory.Agents...)
		if q.state.PolicyKnown && len(q.state.Config.LeaveOut) > 0 {
			// A stat-only walk cannot associate every source with its repo or
			// working directory. Suppress window counts under leave-out rules
			// until parsed, policy-filtered counts are available.
			inventory.Windows = hubclient.InventoryWindows{}
		}
		progress.Inventory = &inventory
	}
	return progress
}

func (q *Queue) NextCheckInDelay() time.Duration {
	q.mu.Lock()
	seconds := q.state.NextCheckInSeconds
	q.mu.Unlock()
	return BaseCheckInCadence(seconds)
}

func (q *Queue) PlanStartedAt() time.Time {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.PlanStartedAt <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(q.state.PlanStartedAt)
}

func (q *Queue) InPlanScope(entry Entry, plan hubclient.V4ImportPlan) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.Config.ImportPlan == nil || q.state.Config.ImportPlan.Version != plan.Version {
		return false
	}
	startedAt := time.Time{}
	if q.state.PlanStartedAt > 0 {
		startedAt = time.UnixMilli(q.state.PlanStartedAt)
	}
	return inScope(entry, plan, startedAt)
}

func pending(entry Entry) bool {
	return entry.RevisionID == "" || entry.SyncedActivity < entry.Activity || entry.SourceRevision != "" && entry.SyncedSourceRevision != entry.SourceRevision
}

func (q *Queue) save() error {
	data, err := json.Marshal(q.state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(q.path), ".queue-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := protectQueueFile(file.Name(), file); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), q.path)
}
