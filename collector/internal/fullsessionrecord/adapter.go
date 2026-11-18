// Package fullsessionrecord maps the private parser model to the public,
// storage-neutral complete-record contract and back.
package fullsessionrecord

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// FromParsedFamily runs portable composition on a clone before freezing one
// complete record for every rooted family member.
func FromParsedFamily(ctx context.Context, sourceID, vendor string, source vendors.ReadSource, parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata) ([]fullsessionv1.Record, error) {
	records, _, err := FromParsedFamilyWithServability(ctx, "", sourceID, vendor, source, parsed, metadata)
	return records, err
}

// FromParsedFamilyWithServability composes once and reports whether the
// family root survives composition even when freezing a complete record fails.
func FromParsedFamilyWithServability(
	ctx context.Context,
	familyID, sourceID, vendor string,
	source vendors.ReadSource,
	parsed []*vendors.ParsedSession,
	metadata *vendors.SessionMetadata,
) ([]fullsessionv1.Record, bool, error) {
	composed, err := composePortableFamily(ctx, vendor, source, parsed, metadata)
	if err != nil {
		return nil, false, err
	}
	servable := false
	for _, item := range composed {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if item.Session.ID == familyID {
			servable = true
			break
		}
	}
	for _, item := range parsed {
		if err := ctx.Err(); err != nil {
			return nil, servable, err
		}
		if vendor != vendors.AgentGrok || item == nil || item.Session == nil {
			continue
		}
		for model := range item.RecordedModelCosts {
			if _, present := item.Session.Tokens[model]; !present {
				return nil, servable, fmt.Errorf("model cost without token counters cannot be represented by full-session/v1")
			}
		}
	}
	records := make([]fullsessionv1.Record, 0, len(composed))
	for _, item := range composed {
		if err := ctx.Err(); err != nil {
			return nil, servable, err
		}
		record, err := fromSession(sourceID, item.ParentSessionID, *item.Session)
		if err != nil {
			return nil, servable, err
		}
		if err := ctx.Err(); err != nil {
			return nil, servable, err
		}
		records = append(records, record)
	}
	return records, servable, nil
}

// IsServableFamily reports whether portable composition retains the family's
// root. Session-meta-only and synthesis-generated roots are intentionally not
// durable remote families.
func IsServableFamily(ctx context.Context, familyID, vendor string, source vendors.ReadSource, parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata) (bool, error) {
	composed, err := composePortableFamily(ctx, vendor, source, parsed, metadata)
	if err != nil {
		return false, err
	}
	for _, item := range composed {
		if item.Session.ID == familyID {
			return true, nil
		}
	}
	return false, nil
}

func composePortableFamily(ctx context.Context, vendor string, source vendors.ReadSource, parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata) ([]collector.PortableSession, error) {
	cloned, err := cloneParsedFamily(ctx, parsed)
	if err != nil {
		return nil, err
	}
	return collector.ComposePortable(ctx, source, map[string]vendors.RemoteCollection{
		vendor: {Sessions: cloned, Metadata: metadata},
	})
}

func FromSession(sourceID string, value session.Session) (fullsessionv1.Record, error) {
	return fromSession(sourceID, value.ParentSessionID, value)
}

func fromSession(sourceID, parentSessionID string, value session.Session) (fullsessionv1.Record, error) {
	pi := value.Agent == vendors.AgentPi
	if !pi && (value.TokensUnavailable || value.CostUnavailable || value.DetailsIncomplete) || value.UnattributedTokens != nil && value.Agent != vendors.AgentGrok && !pi {
		return fullsessionv1.Record{}, fmt.Errorf("local session facts cannot be represented by full-session/v1")
	}
	if pi && (value.TokensUnavailable && (value.TokensKnown || len(value.Tokens) != 0 || value.UnattributedTokens != nil) || value.CostUnavailable && value.Cost != nil) {
		return fullsessionv1.Record{}, fmt.Errorf("inconsistent Pi accounting availability")
	}
	if pi {
		for _, tokens := range value.Tokens {
			if !tokens.CostKnown && tokens.Cost != 0 {
				return fullsessionv1.Record{}, fmt.Errorf("Pi model cost without availability")
			}
		}
	}
	for _, child := range value.Subagents {
		if child.TokensUnavailable || child.CostUnavailable || (child.UnattributedTokens != nil && value.Agent != "grok") {
			return fullsessionv1.Record{}, fmt.Errorf("subagent accounting cannot be represented by full-session/v1")
		}
	}
	for _, row := range value.Digest {
		if !pi && (row.SourceEntryID != "" || row.ParentEntryID != "" || row.BranchID != "" || row.Active != nil || row.Inherited != nil || row.ContextSelected != nil || row.ContextDescription != nil) {
			return fullsessionv1.Record{}, fmt.Errorf("branch context cannot be represented by full-session/v1")
		}
	}
	unattributed, err := unattributedFromTokens(value.UnattributedTokens, value.Cost, pi)
	if err != nil {
		return fullsessionv1.Record{}, err
	}
	record := fullsessionv1.Record{
		SourceID: sourceID, Agent: value.Agent, SessionID: value.ID, ParentSessionID: parentSessionID,
		Session: fullsessionv1.Session{
			Name: session.ClonePointer(value.Name), Summary: session.ClonePointer(value.Summary), Status: session.ClonePointer(value.Status),
			WorkingDirectory: value.WorkingDirectory, Branch: session.ClonePointer(value.Branch), EditedFileCount: value.EditedFileCount,
			DurationMs: session.ClonePointer(value.DurationMs), CostMicroUSD: optionalMicros(value.Cost),
			UnpricedModels: slices.Clone(value.UnpricedModels), StartedAtMs: value.StartedAt,
			LastActivityAtMs: value.LastActivityTime, Entrypoint: session.ClonePointer(value.Entrypoint), Model: session.ClonePointer(value.Model),
			ContextTokens: session.ClonePointer(value.ContextTokens), ContextWindow: session.ClonePointer(value.ContextWindow), Turns: value.Turns,
			ToolUses: value.ToolUses, Errors: value.Errors, Compactions: value.Compactions, FirstPrompt: session.ClonePointer(value.FirstPrompt),
			Commands: slices.Clone(value.Commands), CommandOutputs: cloneOutputs(value.CommandOutputs, len(value.Commands)),
			Skills:  slices.Clone(value.Skills),
			Commits: slices.Clone(value.Commits), CommitSHAs: slices.Clone(value.CommitSHAs), PullRequests: value.PullRequests,
			SynthesisPending:  value.SynthesisPending,
			DeclaredGoal:      session.ClonePointer(value.DeclaredGoal),
			DetailsIncomplete: value.DetailsIncomplete,
		},
	}
	if len(value.PullRequestLinks) > 0 {
		record.Session.PullRequestLinks = make([]fullsessionv1.PullRequestLink, len(value.PullRequestLinks))
		for i, link := range value.PullRequestLinks {
			record.Session.PullRequestLinks[i] = fullsessionv1.PullRequestLink(link)
		}
	}
	record.Session.Usage = usageFromTokens(value.Tokens, pi)
	if pi && value.TokensUnavailable {
		record.Session.Usage = nil
	} else if pi && value.TokensKnown && record.Session.Usage == nil && unattributed == nil {
		record.Session.Usage = []fullsessionv1.ModelUsage{}
	}
	record.Session.UnattributedTokens = unattributed
	if value.Subagents != nil {
		record.Session.Subagents = make([]fullsessionv1.Subagent, 0, len(value.Subagents))
	}
	for _, value := range value.Subagents {
		unattributed, err := unattributedFromTokens(value.UnattributedTokens, value.Cost, false)
		if err != nil {
			return fullsessionv1.Record{}, err
		}
		item := fullsessionv1.Subagent{
			ID: value.ID, Name: value.Name, Model: session.ClonePointer(value.Model), Status: value.Status,
			Task: value.Task, Result: value.Result, DurationMs: session.ClonePointer(value.DurationMs),
			SpawnedAtTurn: session.ClonePointer(value.SpawnedAtTurn), ToolUses: value.ToolUses,
			Usage: usageFromTokens(value.Tokens, false), CostMicroUSD: optionalMicros(value.Cost), UnattributedTokens: unattributed,
		}
		if value.Commands != nil {
			item.Commands = make([]fullsessionv1.SubagentCommand, 0, len(value.Commands))
		}
		for _, command := range value.Commands {
			item.Commands = append(item.Commands, fullsessionv1.SubagentCommand{Label: command.Label, Command: command.Command, Output: session.ClonePointer(command.Output)})
		}
		record.Session.Subagents = append(record.Session.Subagents, item)
	}
	if value.Todos != nil {
		record.Session.Todos = make([]fullsessionv1.Todo, 0, len(value.Todos))
	}
	for _, value := range value.Todos {
		record.Session.Todos = append(record.Session.Todos, fullsessionv1.Todo{Text: value.Text, Done: value.Done})
	}
	if value.Digest != nil {
		record.Session.Digest = make([]fullsessionv1.DigestEntry, 0, len(value.Digest))
	}
	for _, value := range value.Digest {
		record.Session.Digest = append(record.Session.Digest, fullsessionv1.DigestEntry{
			Turn: value.Turn, Category: value.Category, Description: value.Description, Answer: value.Answer,
			SubagentID: value.SubagentID, TimeMs: value.Time,
			SourceEntryID: value.SourceEntryID, ParentEntryID: value.ParentEntryID, BranchID: value.BranchID,
			Inherited: session.ClonePointer(value.Inherited), Active: session.ClonePointer(value.Active), ContextSelected: session.ClonePointer(value.ContextSelected), ContextDescription: session.ClonePointer(value.ContextDescription),
		})
	}
	if value.FileEdits != nil {
		record.Session.FileEdits = make([]fullsessionv1.FileEdit, 0, len(value.FileEdits))
	}
	for _, value := range value.FileEdits {
		item := fullsessionv1.FileEdit{
			Path: value.Path, Additions: value.Additions, Deletions: value.Deletions, Edits: value.Edits, IsNew: value.IsNew,
		}
		changes := value.Changes()
		if changes != nil {
			item.Changes = make([]fullsessionv1.FileChange, 0, len(changes))
		}
		for _, change := range changes {
			item.Changes = append(item.Changes, fullsessionv1.FileChange{
				ID: change.ID, Kind: change.Kind, Text: change.Text, Operation: change.Operation,
				Additions: change.Additions, Deletions: change.Deletions,
			})
		}
		record.Session.FileEdits = append(record.Session.FileEdits, item)
	}
	if value.Synthesis != nil {
		record.Session.Synthesis = &fullsessionv1.SessionSynthesis{
			Goals: slices.Clone(value.Synthesis.Goals), Outcome: value.Synthesis.Outcome,
			KeyDecisions: slices.Clone(value.Synthesis.KeyDecisions), NextStep: value.Synthesis.NextStep,
		}
	}
	frozen, err := fullsessionv1.Freeze(record)
	if err != nil && hasCommandOutputs(record.Session) {
		// Outputs are optional detail. A record that outgrows a bound with them
		// still syncs without them, as an uncollected-outputs record.
		record.Session.CommandOutputs = nil
		record.Session.Subagents = slices.Clone(record.Session.Subagents)
		for i := range record.Session.Subagents {
			record.Session.Subagents[i].Commands = slices.Clone(record.Session.Subagents[i].Commands)
			for j := range record.Session.Subagents[i].Commands {
				record.Session.Subagents[i].Commands[j].Output = nil
			}
		}
		return fullsessionv1.Freeze(record)
	}
	return frozen, err
}

func hasCommandOutputs(value fullsessionv1.Session) bool {
	if value.CommandOutputs != nil {
		return true
	}
	for _, child := range value.Subagents {
		for _, command := range child.Commands {
			if command.Output != nil {
				return true
			}
		}
	}
	return false
}

func ToSession(record fullsessionv1.Record) (*session.Session, error) {
	if err := fullsessionv1.Validate(record); err != nil {
		return nil, err
	}
	pi := record.Agent == vendors.AgentPi
	value := &session.Session{
		Agent: record.Agent, ID: record.SessionID, ParentSessionID: record.ParentSessionID,
		Name: session.ClonePointer(record.Session.Name), Summary: session.ClonePointer(record.Session.Summary),
		Status: session.ClonePointer(record.Session.Status), WorkingDirectory: record.Session.WorkingDirectory,
		Branch: session.ClonePointer(record.Session.Branch), EditedFileCount: record.Session.EditedFileCount,
		DurationMs: session.ClonePointer(record.Session.DurationMs), Tokens: tokensFromUsage(record.Session.Usage, pi),
		UnattributedTokens: tokensFromUnattributed(record.Session.UnattributedTokens, pi),
		Cost:               optionalDollars(record.Session.CostMicroUSD), UnpricedModels: slices.Clone(record.Session.UnpricedModels),
		StartedAt: record.Session.StartedAtMs, LastActivityTime: record.Session.LastActivityAtMs,
		Entrypoint: session.ClonePointer(record.Session.Entrypoint),
		SessionDetails: session.SessionDetails{
			Model: session.ClonePointer(record.Session.Model), ContextTokens: session.ClonePointer(record.Session.ContextTokens),
			ContextWindow: session.ClonePointer(record.Session.ContextWindow), Turns: record.Session.Turns,
			ToolUses: record.Session.ToolUses, Errors: record.Session.Errors, Compactions: record.Session.Compactions,
			FirstPrompt: session.ClonePointer(record.Session.FirstPrompt), Commands: slices.Clone(record.Session.Commands),
			CommandOutputs: cloneOutputs(record.Session.CommandOutputs, len(record.Session.Commands)),
			Skills:         slices.Clone(record.Session.Skills),
			Commits:        slices.Clone(record.Session.Commits), CommitSHAs: slices.Clone(record.Session.CommitSHAs),
			PullRequests:     record.Session.PullRequests,
			SynthesisPending: record.Session.SynthesisPending, DeclaredGoal: session.ClonePointer(record.Session.DeclaredGoal),
			DetailsIncomplete: record.Session.DetailsIncomplete,
		},
	}
	if pi {
		value.TokensKnown = record.Session.Usage != nil || record.Session.UnattributedTokens != nil
		value.TokensUnavailable = !value.TokensKnown
		value.CostUnavailable = record.Session.CostMicroUSD == nil
	}
	for _, link := range record.Session.PullRequestLinks {
		value.PullRequestLinks = append(value.PullRequestLinks, session.PullRequestLink(link))
	}
	if record.Session.Subagents != nil {
		value.Subagents = make([]session.Subagent, 0, len(record.Session.Subagents))
	}
	for _, item := range record.Session.Subagents {
		subagent := session.Subagent{
			ID: item.ID, Name: item.Name, Model: session.ClonePointer(item.Model), Status: item.Status, Task: item.Task,
			Result: item.Result, DurationMs: session.ClonePointer(item.DurationMs), SpawnedAtTurn: session.ClonePointer(item.SpawnedAtTurn),
			UnattributedTokens: tokensFromUnattributed(item.UnattributedTokens, false),
			ToolUses:           item.ToolUses, Tokens: tokensFromUsage(item.Usage, false), Cost: optionalDollars(item.CostMicroUSD),
		}
		if item.Commands != nil {
			subagent.Commands = make([]session.SubagentCommand, 0, len(item.Commands))
		}
		for _, command := range item.Commands {
			subagent.Commands = append(subagent.Commands, session.SubagentCommand{Label: command.Label, Command: command.Command, Output: session.ClonePointer(command.Output)})
		}
		value.Subagents = append(value.Subagents, subagent)
	}
	if record.Session.Todos != nil {
		value.Todos = make([]session.Todo, 0, len(record.Session.Todos))
	}
	for _, item := range record.Session.Todos {
		value.Todos = append(value.Todos, session.Todo{Text: item.Text, Done: item.Done})
	}
	if record.Session.Digest != nil {
		value.Digest = make([]session.DigestEntry, 0, len(record.Session.Digest))
	}
	for _, item := range record.Session.Digest {
		value.Digest = append(value.Digest, session.DigestEntry{
			Turn: item.Turn, Category: item.Category, Description: item.Description, Answer: item.Answer,
			SubagentID: item.SubagentID, Time: item.TimeMs,
			SourceEntryID: item.SourceEntryID, ParentEntryID: item.ParentEntryID, BranchID: item.BranchID,
			Inherited: session.ClonePointer(item.Inherited), Active: session.ClonePointer(item.Active), ContextSelected: session.ClonePointer(item.ContextSelected), ContextDescription: session.ClonePointer(item.ContextDescription),
		})
	}
	if record.Session.FileEdits != nil {
		value.FileEdits = make([]session.FileEdit, 0, len(record.Session.FileEdits))
	}
	for _, item := range record.Session.FileEdits {
		var changes []session.FileChange
		var changeIDs []string
		if item.Changes != nil {
			changes = make([]session.FileChange, 0, len(item.Changes))
			changeIDs = make([]string, 0, len(item.Changes))
		}
		for _, change := range item.Changes {
			changeIDs = append(changeIDs, change.ID)
			changes = append(changes, session.FileChange{
				ID: change.ID, Kind: change.Kind, Text: change.Text, Operation: change.Operation,
				Additions: change.Additions, Deletions: change.Deletions,
			})
		}
		value.FileEdits = append(value.FileEdits, session.FileEditWithIdentifiedChanges(
			item.Path, item.Additions, item.Deletions, item.Edits, item.IsNew, changeIDs, changes,
		))
	}
	if record.Session.Synthesis != nil {
		value.Synthesis = &session.SessionSynthesis{
			Goals: slices.Clone(record.Session.Synthesis.Goals), Outcome: record.Session.Synthesis.Outcome,
			KeyDecisions: slices.Clone(record.Session.Synthesis.KeyDecisions), NextStep: record.Session.Synthesis.NextStep,
		}
	}
	return value, nil
}

func unattributedFromTokens(tokens *session.ModelTokens, cost *float64, pi bool) (*fullsessionv1.UnattributedUsage, error) {
	if tokens == nil {
		return nil, nil
	}
	if pi {
		cost = nil
		if tokens.CostKnown {
			cost = &tokens.Cost
		}
	}
	if (!pi || !tokens.CostKnown) && tokens.Cost != 0 || cost != nil && (math.IsNaN(*cost) || math.IsInf(*cost, 0) || *cost < 0 || *cost > float64(fullsessionv1.MaxCostMicroUSD)/1_000_000) {
		return nil, fmt.Errorf("invalid unattributed accounting cost")
	}
	return &fullsessionv1.UnattributedUsage{
		InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens, CacheCreationInputTokens: tokens.CacheCreationInputTokens,
		CacheCreation1hInputTokens: tokens.CacheCreation1hInputTokens, CacheReadInputTokens: tokens.CacheReadInputTokens, CostMicroUSD: optionalMicros(cost),
	}, nil
}

func tokensFromUnattributed(usage *fullsessionv1.UnattributedUsage, pi bool) *session.ModelTokens {
	if usage == nil {
		return nil
	}
	result := &session.ModelTokens{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens, CacheReadInputTokens: usage.CacheReadInputTokens,
	}
	if pi && usage.CostMicroUSD != nil {
		result.CostKnown, result.Cost = true, dollars(*usage.CostMicroUSD)
	}
	return result
}

func usageFromTokens(tokens map[string]session.ModelTokens, pi bool) []fullsessionv1.ModelUsage {
	if tokens == nil {
		return nil
	}
	usage := make([]fullsessionv1.ModelUsage, 0, len(tokens))
	models := make([]string, 0, len(tokens))
	for model := range tokens {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		tokens := tokens[model]
		cost := optionalMicros(&tokens.Cost)
		if pi && !tokens.CostKnown {
			cost = nil
		}
		usage = append(usage, fullsessionv1.ModelUsage{
			Model: model, InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens,
			CacheCreationInputTokens:   tokens.CacheCreationInputTokens,
			CacheCreation1hInputTokens: tokens.CacheCreation1hInputTokens,
			CacheReadInputTokens:       tokens.CacheReadInputTokens, CostMicroUSD: cost,
		})
	}
	return usage
}

func tokensFromUsage(usage []fullsessionv1.ModelUsage, pi bool) map[string]session.ModelTokens {
	if usage == nil {
		return nil
	}
	tokens := make(map[string]session.ModelTokens, len(usage))
	for _, usage := range usage {
		tokens[usage.Model] = session.ModelTokens{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheCreationInputTokens:   usage.CacheCreationInputTokens,
			CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens,
			CacheReadInputTokens:       usage.CacheReadInputTokens,
		}
		if usage.CostMicroUSD != nil {
			used := tokens[usage.Model]
			used.CostKnown = pi
			used.Cost = dollars(*usage.CostMicroUSD)
			tokens[usage.Model] = used
		}
	}
	return tokens
}

func cloneParsedFamily(ctx context.Context, parsed []*vendors.ParsedSession) ([]*vendors.ParsedSession, error) {
	cloned := make([]*vendors.ParsedSession, 0, len(parsed))
	for _, item := range parsed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item == nil {
			cloned = append(cloned, nil)
			continue
		}
		copy := *item
		copy.Session = session.Clone(item.Session)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		copy.Spawns = make(map[string]vendors.SpawnState, len(item.Spawns))
		for key, spawn := range item.Spawns {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if spawn.Turn != nil {
				turn := *spawn.Turn
				spawn.Turn = &turn
			}
			copy.Spawns[key] = spawn
		}
		copy.RecordedModelCosts = maps.Clone(item.RecordedModelCosts)
		copy.Commands = slices.Clone(item.Commands)
		copy.StatusHint = session.ClonePointer(item.StatusHint)
		if item.RecordedCost != nil {
			cost := *item.RecordedCost
			copy.RecordedCost = &cost
		}
		cloned = append(cloned, &copy)
	}
	return cloned, nil
}

func micros(value float64) int64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return -1
	}
	return int64(math.Round(value * 1_000_000))
}
func dollars(value int64) float64 { return float64(value) / 1_000_000 }
func optionalMicros(value *float64) *int64 {
	if value == nil {
		return nil
	}
	micros := micros(*value)
	return &micros
}
func optionalDollars(value *int64) *float64 {
	if value == nil {
		return nil
	}
	dollars := dollars(*value)
	return &dollars
}

// cloneOutputs deep-copies command outputs. A session without commands omits
// the field, because the portable record rejects an empty array there.
func cloneOutputs(outputs []*string, commands int) []*string {
	if outputs == nil || commands == 0 {
		return nil
	}
	cloned := make([]*string, len(outputs))
	for i, output := range outputs {
		cloned[i] = session.ClonePointer(output)
	}
	return cloned
}
