// Package fullsessionrecord maps the private parser model to the public,
// storage-neutral complete-record contract and back.
package fullsessionrecord

import (
	"math"
	"sort"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// FromParsedFamily runs the same shared composition used by local and remote
// library sessions before freezing the rooted complete records.
func FromParsedFamily(sourceID, vendor string, source vendors.ReadSource, parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata) ([]fullsessionv1.Record, error) {
	roots := collector.ListRemote(source, map[string]vendors.RemoteCollection{
		vendor: {Sessions: parsed, Metadata: metadata},
	}, 0)
	records := make([]fullsessionv1.Record, 0, len(roots))
	for _, root := range roots {
		record, err := FromSession(sourceID, *root)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func FromSession(sourceID string, value session.Session) (fullsessionv1.Record, error) {
	record := fullsessionv1.Record{
		SourceID: sourceID, Agent: value.Agent, SessionID: value.ID,
		Session: fullsessionv1.Session{
			Name: cloneString(value.Name), Summary: cloneString(value.Summary), Status: cloneString(value.Status),
			WorkingDirectory: value.WorkingDirectory, Branch: cloneString(value.Branch), EditedFileCount: value.EditedFileCount,
			DurationMs: cloneInt(value.DurationMs), CostMicroUSD: optionalMicros(value.Cost),
			UnpricedModels: append([]string{}, value.UnpricedModels...), StartedAtMs: value.StartedAt,
			LastActivityAtMs: value.LastActivityTime, Entrypoint: cloneString(value.Entrypoint), Model: cloneString(value.Model),
			ContextTokens: cloneInt(value.ContextTokens), ContextWindow: cloneInt(value.ContextWindow), Turns: value.Turns,
			ToolUses: value.ToolUses, Errors: value.Errors, Compactions: value.Compactions, FirstPrompt: cloneString(value.FirstPrompt),
			Commands: append([]string{}, value.Commands...), Commits: append([]string{}, value.Commits...),
			CommitSHAs: append([]string{}, value.CommitSHAs...), PullRequests: value.PullRequests,
			SynthesisPending: value.SynthesisPending,
			DeclaredGoal:     cloneString(value.DeclaredGoal),
		},
	}
	models := make([]string, 0, len(value.Tokens))
	for model := range value.Tokens {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		tokens := value.Tokens[model]
		record.Session.Usage = append(record.Session.Usage, fullsessionv1.ModelUsage{
			Model: model, InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens,
			CacheCreationInputTokens:   tokens.CacheCreationInputTokens,
			CacheCreation1hInputTokens: tokens.CacheCreation1hInputTokens,
			CacheReadInputTokens:       tokens.CacheReadInputTokens, CostMicroUSD: micros(tokens.Cost),
		})
	}
	for _, value := range value.Subagents {
		item := fullsessionv1.Subagent{
			ID: value.ID, Name: value.Name, Model: cloneString(value.Model), Status: value.Status,
			Task: value.Task, Result: value.Result, DurationMs: cloneInt(value.DurationMs),
			SpawnedAtTurn: cloneInt(value.SpawnedAtTurn), ToolUses: value.ToolUses,
			CostMicroUSD: optionalMicros(value.Cost),
		}
		for _, command := range value.Commands {
			item.Commands = append(item.Commands, fullsessionv1.SubagentCommand{Label: command.Label, Command: command.Command})
		}
		models = models[:0]
		for model := range value.Tokens {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			tokens := value.Tokens[model]
			item.Usage = append(item.Usage, fullsessionv1.ModelUsage{
				Model: model, InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens,
				CacheCreationInputTokens:   tokens.CacheCreationInputTokens,
				CacheCreation1hInputTokens: tokens.CacheCreation1hInputTokens,
				CacheReadInputTokens:       tokens.CacheReadInputTokens, CostMicroUSD: micros(tokens.Cost),
			})
		}
		record.Session.Subagents = append(record.Session.Subagents, item)
	}
	for _, value := range value.Todos {
		record.Session.Todos = append(record.Session.Todos, fullsessionv1.Todo{Text: value.Text, Done: value.Done})
	}
	for _, value := range value.Digest {
		record.Session.Digest = append(record.Session.Digest, fullsessionv1.DigestEntry{
			Turn: value.Turn, Category: value.Category, Description: value.Description, Answer: value.Answer,
			SubagentID: value.SubagentID, TimeMs: value.Time,
		})
	}
	for _, value := range value.FileEdits {
		item := fullsessionv1.FileEdit{
			Path: value.Path, Additions: value.Additions, Deletions: value.Deletions, Edits: value.Edits, IsNew: value.IsNew,
		}
		for _, change := range value.Changes() {
			item.Changes = append(item.Changes, fullsessionv1.FileChange{
				Kind: change.Kind, Text: change.Text, Operation: change.Operation,
				Additions: change.Additions, Deletions: change.Deletions,
			})
		}
		record.Session.FileEdits = append(record.Session.FileEdits, item)
	}
	if value.Synthesis != nil {
		record.Session.Synthesis = &fullsessionv1.SessionSynthesis{
			Goals: append([]string{}, value.Synthesis.Goals...), Outcome: value.Synthesis.Outcome,
			KeyDecisions: append([]string{}, value.Synthesis.KeyDecisions...), NextStep: value.Synthesis.NextStep,
		}
	}
	return fullsessionv1.Freeze(record)
}

func ToSession(record fullsessionv1.Record) (*session.Session, error) {
	if err := fullsessionv1.Validate(record); err != nil {
		return nil, err
	}
	value := &session.Session{
		Agent: record.Agent, ID: record.SessionID, Name: cloneString(record.Session.Name), Summary: cloneString(record.Session.Summary),
		Status: cloneString(record.Session.Status), WorkingDirectory: record.Session.WorkingDirectory,
		Branch: cloneString(record.Session.Branch), EditedFileCount: record.Session.EditedFileCount,
		DurationMs: cloneInt(record.Session.DurationMs), Tokens: map[string]session.ModelTokens{},
		Cost: optionalDollars(record.Session.CostMicroUSD), UnpricedModels: append([]string{}, record.Session.UnpricedModels...),
		StartedAt: record.Session.StartedAtMs, LastActivityTime: record.Session.LastActivityAtMs,
		Entrypoint: cloneString(record.Session.Entrypoint),
		SessionDetails: session.SessionDetails{
			Model: cloneString(record.Session.Model), ContextTokens: cloneInt(record.Session.ContextTokens),
			ContextWindow: cloneInt(record.Session.ContextWindow), Turns: record.Session.Turns,
			ToolUses: record.Session.ToolUses, Errors: record.Session.Errors, Compactions: record.Session.Compactions,
			FirstPrompt: cloneString(record.Session.FirstPrompt), Commands: append([]string{}, record.Session.Commands...),
			Commits: append([]string{}, record.Session.Commits...), CommitSHAs: append([]string{}, record.Session.CommitSHAs...),
			PullRequests:     record.Session.PullRequests,
			SynthesisPending: record.Session.SynthesisPending, DeclaredGoal: cloneString(record.Session.DeclaredGoal),
		},
	}
	for _, usage := range record.Session.Usage {
		value.Tokens[usage.Model] = session.ModelTokens{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheCreationInputTokens:   usage.CacheCreationInputTokens,
			CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens,
			CacheReadInputTokens:       usage.CacheReadInputTokens, Cost: dollars(usage.CostMicroUSD),
		}
	}
	for _, item := range record.Session.Subagents {
		subagent := session.Subagent{
			ID: item.ID, Name: item.Name, Model: cloneString(item.Model), Status: item.Status, Task: item.Task,
			Result: item.Result, DurationMs: cloneInt(item.DurationMs), SpawnedAtTurn: cloneInt(item.SpawnedAtTurn),
			ToolUses: item.ToolUses, Tokens: map[string]session.ModelTokens{}, Cost: optionalDollars(item.CostMicroUSD),
		}
		for _, command := range item.Commands {
			subagent.Commands = append(subagent.Commands, session.SubagentCommand{Label: command.Label, Command: command.Command})
		}
		for _, usage := range item.Usage {
			subagent.Tokens[usage.Model] = session.ModelTokens{
				InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
				CacheCreationInputTokens:   usage.CacheCreationInputTokens,
				CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens,
				CacheReadInputTokens:       usage.CacheReadInputTokens, Cost: dollars(usage.CostMicroUSD),
			}
		}
		value.Subagents = append(value.Subagents, subagent)
	}
	for _, item := range record.Session.Todos {
		value.Todos = append(value.Todos, session.Todo{Text: item.Text, Done: item.Done})
	}
	for _, item := range record.Session.Digest {
		value.Digest = append(value.Digest, session.DigestEntry{
			Turn: item.Turn, Category: item.Category, Description: item.Description, Answer: item.Answer,
			SubagentID: item.SubagentID, Time: item.TimeMs,
		})
	}
	for _, item := range record.Session.FileEdits {
		changes := make([]session.FileChange, 0, len(item.Changes))
		for _, change := range item.Changes {
			changes = append(changes, session.FileChange{
				Kind: change.Kind, Text: change.Text, Operation: change.Operation,
				Additions: change.Additions, Deletions: change.Deletions,
			})
		}
		value.FileEdits = append(value.FileEdits, session.FileEditWithChanges(
			item.Path, item.Additions, item.Deletions, item.Edits, item.IsNew, changes,
		))
	}
	if record.Session.Synthesis != nil {
		value.Synthesis = &session.SessionSynthesis{
			Goals: append([]string{}, record.Session.Synthesis.Goals...), Outcome: record.Session.Synthesis.Outcome,
			KeyDecisions: append([]string{}, record.Session.Synthesis.KeyDecisions...), NextStep: record.Session.Synthesis.NextStep,
		}
	}
	return value, nil
}

func micros(value float64) int64  { return int64(math.Round(value * 1_000_000)) }
func dollars(value int64) float64 { return float64(value) / 1_000_000 }
func optionalMicros(value *float64) int64 {
	if value == nil {
		return 0
	}
	return micros(*value)
}
func optionalDollars(value int64) *float64 {
	dollars := dollars(value)
	return &dollars
}
func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
