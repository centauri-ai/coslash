package inventory

import (
	"context"
	"encoding/json"
	"iter"
	"log"
	"sort"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	"github.com/centauri-ai/coslash/collector/internal/vendors/cursor"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

const defaultBatchFamilies = 64

// Cursor is the persisted position of a streamed discovery pass. Families
// are visited newest first; on resume, a family already visited is skipped
// unless its activity is at or after StartedAtMs, which means it changed
// after the pass began.
type Cursor struct {
	StartedAtMs int64  `json:"startedAtMs"`
	ActivityMs  int64  `json:"activityMs"`
	Agent       string `json:"agent"`
	Family      string `json:"family"`
	Complete    bool   `json:"complete"`
}

// DiscoverOptions configures one streamed pass.
type DiscoverOptions struct {
	// Snapshot is the stat-only inventory to plan from; nil scans now.
	Snapshot *Snapshot
	// MinActivityMs skips parsing families whose newest source is older than
	// this timestamp. Zero includes all history.
	MinActivityMs int64
	// Resume continues an incomplete earlier pass.
	Resume *Cursor
	// BatchFamilies bounds how many families are parsed before a yield.
	BatchFamilies int
	// Persist is called with the cursor after every yielded batch and once
	// more with Complete set; a nil Persist keeps the cursor in memory.
	Persist func(Cursor) error
}

// Batch is one yielded slice of finalized root sessions, newest first.
type Batch struct {
	Sessions []*session.Session
	// ContentBytes is a bounded source-byte estimate by agent and root ID.
	// The scheduler uses it before a full backup has been captured.
	ContentBytes map[string]int64
	Cursor       Cursor
	Remaining    int
}

// DecodeCursor restores a persisted cursor; invalid input yields no cursor.
func DecodeCursor(payload json.RawMessage) *Cursor {
	if len(payload) == 0 {
		return nil
	}
	var cursor Cursor
	if json.Unmarshal(payload, &cursor) != nil || cursor.StartedAtMs == 0 {
		return nil
	}
	return &cursor
}

type family struct {
	agent      string
	id         string
	activityMs int64
	bytes      int64
	files      []string
}

func familyBefore(a, b family) bool {
	if a.activityMs != b.activityMs {
		return a.activityMs > b.activityMs
	}
	if a.agent != b.agent {
		return a.agent < b.agent
	}
	return a.id < b.id
}

// Discover streams finalized root sessions newest first, parsing each family
// once through the parse cache and yielding after every batch. Composition
// runs per batch, so a family is never split across yields.
func Discover(ctx context.Context, opts DiscoverOptions) iter.Seq2[Batch, error] {
	return func(yield func(Batch, error) bool) {
		batches, err := planDiscovery(ctx, opts)
		if err != nil {
			yield(Batch{}, err)
			return
		}
		batches.run(ctx, yield)
	}
}

// DiscoverAll drains a streamed pass into one list.
func DiscoverAll(ctx context.Context, opts DiscoverOptions) ([]*session.Session, error) {
	sessions := []*session.Session{}
	for batch, err := range Discover(ctx, opts) {
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, batch.Sessions...)
	}
	return sessions, nil
}

type discoveryPlan struct {
	families []family
	metadata map[string]*vendors.SessionMetadata
	cursor   Cursor
	batch    int
	persist  func(Cursor) error
	live     map[string]string
}

func planDiscovery(ctx context.Context, opts DiscoverOptions) (*discoveryPlan, error) {
	snapshot := opts.Snapshot
	if snapshot == nil {
		var err error
		if snapshot, err = Scan(ctx, Options{}); err != nil {
			return nil, err
		}
	}
	if opts.MinActivityMs > 0 {
		newest := make(map[string]int64, len(snapshot.Files))
		for _, file := range snapshot.Files {
			key := file.Agent + "\x00" + file.FamilyID
			newest[key] = max(newest[key], file.ModTimeMs)
		}
		filtered := make([]File, 0, len(snapshot.Files))
		for _, file := range snapshot.Files {
			if file.Agent == vendors.AgentOpenCode || newest[file.Agent+"\x00"+file.FamilyID] >= opts.MinActivityMs {
				filtered = append(filtered, file)
			}
		}
		copy := *snapshot
		copy.Files = filtered
		snapshot = &copy
	}
	byAgent := map[string][]string{}
	for _, file := range snapshot.Files {
		if file.Agent != vendors.AgentOpenCode {
			byAgent[file.Agent] = append(byAgent[file.Agent], file.Path)
		}
	}
	activity := make(map[string]int64, len(snapshot.Files))
	sizes := make(map[string]int64, len(snapshot.Files))
	for _, file := range snapshot.Files {
		activity[file.Path] = file.ModTimeMs
		sizes[file.Path] = file.Size
	}
	newest := func(files []string) int64 {
		value := int64(0)
		for _, file := range files {
			value = max(value, activity[file])
		}
		return value
	}
	sourceBytes := func(files []string) int64 {
		var total int64
		for _, file := range files {
			total += sizes[file]
		}
		return total
	}

	plan := &discoveryPlan{metadata: map[string]*vendors.SessionMetadata{}, batch: opts.BatchFamilies, persist: opts.Persist}
	if plan.batch <= 0 {
		plan.batch = defaultBatchFamilies
	}
	var failures []error
	// Claude: families follow paths; the parser reads no header.
	if files := byAgent[vendors.AgentClaude]; len(files) > 0 {
		metadata, err := claude.LoadMetadataContext(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			metadata = vendors.BestEffortMetadata(vendors.AgentClaude, func() (*vendors.SessionMetadata, error) { return nil, err })
		}
		plan.metadata[vendors.AgentClaude] = metadata
		grouped := map[string][]string{}
		for _, file := range files {
			id := claude.FamilyIDFromPath(file)
			grouped[id] = append(grouped[id], file)
		}
		for id, members := range grouped {
			plan.families = append(plan.families, family{agent: vendors.AgentClaude, id: id, activityMs: newest(members), bytes: sourceBytes(members), files: members})
		}
	}
	if files := byAgent[vendors.AgentCodex]; len(files) > 0 {
		metadata, err := codex.LoadMetadataForFilesContext(ctx, files)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			metadata = vendors.BestEffortMetadata(vendors.AgentCodex, func() (*vendors.SessionMetadata, error) { return nil, err })
		}
		plan.metadata[vendors.AgentCodex] = metadata
		grouped, err := codex.FamiliesContext(ctx, files)
		if err != nil {
			return nil, err
		}
		for id, members := range grouped {
			plan.families = append(plan.families, family{agent: vendors.AgentCodex, id: id, activityMs: newest(members), bytes: sourceBytes(members), files: members})
		}
	}
	if files := byAgent[vendors.AgentCursor]; len(files) > 0 {
		cursorPlan, err := cursor.PlanFamiliesContext(ctx, files)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failures = append(failures, err)
		} else {
			plan.metadata[vendors.AgentCursor] = cursorPlan.Metadata
			plan.live = cursorPlan.Live
			for id, members := range cursorPlan.Families {
				plan.families = append(plan.families, family{agent: vendors.AgentCursor, id: id, activityMs: newest(members), bytes: sourceBytes(members), files: members})
			}
		}
	}
	refs, err := opencode.FamiliesContext(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		failures = append(failures, err)
	}
	var databaseBytes int64
	for _, file := range snapshot.Files {
		if file.Agent == vendors.AgentOpenCode {
			databaseBytes += file.Size
		}
	}
	for _, ref := range refs {
		if opts.MinActivityMs > 0 && ref.ActivityMs < opts.MinActivityMs {
			continue
		}
		plan.families = append(plan.families, family{agent: vendors.AgentOpenCode, id: ref.ID, activityMs: ref.ActivityMs, bytes: databaseBytes / int64(len(refs))})
	}
	if len(failures) > 0 {
		logDiscoveryFailures(failures)
	}
	sort.SliceStable(plan.families, func(i, j int) bool { return familyBefore(plan.families[i], plan.families[j]) })

	plan.cursor = Cursor{StartedAtMs: time.Now().UnixMilli()}
	if resume := opts.Resume; resume != nil && !resume.Complete && resume.StartedAtMs > 0 {
		plan.cursor = *resume
		position := family{agent: resume.Agent, id: resume.Family, activityMs: resume.ActivityMs}
		kept := plan.families[:0]
		for _, item := range plan.families {
			visited := familyBefore(item, position) || item.agent == position.agent && item.id == position.id
			if visited && item.activityMs < resume.StartedAtMs {
				continue
			}
			kept = append(kept, item)
		}
		plan.families = kept
	}
	return plan, nil
}

func logDiscoveryFailures(failures []error) {
	for _, err := range failures {
		log.Printf("streamed discovery: %v; serving other vendors", err)
	}
}

func (plan *discoveryPlan) run(ctx context.Context, yield func(Batch, error) bool) {
	type sessionKey struct{ agent, id string }
	yielded := map[sessionKey]bool{}
	cursor := plan.cursor
	for start := 0; start < len(plan.families); start += plan.batch {
		if err := ctx.Err(); err != nil {
			yield(Batch{}, err)
			return
		}
		end := min(start+plan.batch, len(plan.families))
		batch := plan.families[start:end]
		sessions, err := plan.parseBatch(ctx, batch)
		if err != nil {
			yield(Batch{}, err)
			return
		}
		kept := make([]*session.Session, 0, len(sessions))
		for _, item := range sessions {
			key := sessionKey{agent: item.Agent, id: item.ID}
			if yielded[key] {
				continue
			}
			yielded[key] = true
			kept = append(kept, item)
		}
		last := batch[len(batch)-1]
		cursor.ActivityMs, cursor.Agent, cursor.Family = last.activityMs, last.agent, last.id
		cursor.Complete = end == len(plan.families)
		familyBytes := make(map[string]int64, len(batch))
		agentBytes := map[string]int64{}
		agentCount := map[string]int64{}
		for _, family := range batch {
			familyBytes[family.agent+"\x00"+family.id] += family.bytes
			agentBytes[family.agent] += family.bytes
		}
		for _, item := range kept {
			agentCount[item.Agent]++
		}
		contentBytes := make(map[string]int64, len(kept))
		for _, item := range kept {
			key := item.Agent + "\x00" + item.ID
			contentBytes[key] = familyBytes[key]
			if contentBytes[key] <= 0 && agentCount[item.Agent] > 0 {
				contentBytes[key] = agentBytes[item.Agent] / agentCount[item.Agent]
			}
		}
		continued := yield(Batch{Sessions: kept, ContentBytes: contentBytes, Cursor: cursor, Remaining: len(plan.families) - end}, nil)
		if plan.persist != nil {
			if err := plan.persist(cursor); err != nil {
				yield(Batch{}, err)
				return
			}
		}
		if !continued {
			return
		}
	}
	if len(plan.families) == 0 {
		cursor.Complete = true
		if plan.persist != nil {
			if err := plan.persist(cursor); err != nil {
				yield(Batch{}, err)
				return
			}
		}
		yield(Batch{Cursor: cursor}, nil)
	}
}

func (plan *discoveryPlan) parseBatch(ctx context.Context, batch []family) ([]*session.Session, error) {
	files := map[string][]string{}
	var opencodeIDs []string
	for _, item := range batch {
		if item.agent == vendors.AgentOpenCode {
			opencodeIDs = append(opencodeIDs, item.id)
			continue
		}
		files[item.agent] = append(files[item.agent], item.files...)
	}
	parsed := []*vendors.ParsedSession{}
	metadata := map[string]*vendors.SessionMetadata{}
	for agent, value := range plan.metadata {
		metadata[agent] = value
	}
	if paths := files[vendors.AgentClaude]; len(paths) > 0 {
		items, err := claude.ParseFilesContext(ctx, paths)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, items...)
	}
	if paths := files[vendors.AgentCodex]; len(paths) > 0 {
		items, err := codex.ParseFilesContext(ctx, paths)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, items...)
	}
	if paths := files[vendors.AgentCursor]; len(paths) > 0 {
		items, batchMetadata, err := cursor.ParseFilesContext(ctx, paths, plan.live)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, items...)
		metadata[vendors.AgentCursor] = mergeMetadata(plan.metadata[vendors.AgentCursor], batchMetadata)
	}
	if len(opencodeIDs) > 0 {
		items, dbMetadata, err := opencode.LoadFamiliesContext(ctx, opencodeIDs)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, items...)
		if plan.metadata[vendors.AgentOpenCode] == nil {
			plan.metadata[vendors.AgentOpenCode] = dbMetadata
		}
		metadata[vendors.AgentOpenCode] = plan.metadata[vendors.AgentOpenCode]
	}
	return collector.FinalizeBatchContext(ctx, parsed, metadata)
}

// mergeMetadata overlays batch metadata on the pass-level metadata without
// mutating either.
func mergeMetadata(base, overlay *vendors.SessionMetadata) *vendors.SessionMetadata {
	merged := vendors.EmptySessionMetadata()
	for _, source := range []*vendors.SessionMetadata{base, overlay} {
		if source == nil {
			continue
		}
		for id, enrichment := range source.Sessions {
			merged.Sessions[id] = enrichment
		}
	}
	return merged
}
