package session

import (
	"maps"
	"slices"
)

// Clone returns a deep copy suitable for composition stages that mutate a
// parsed session while deriving display or portable-record fields.
func Clone(source *Session) *Session {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Name = ClonePointer(source.Name)
	cloned.Summary = ClonePointer(source.Summary)
	cloned.Status = ClonePointer(source.Status)
	cloned.RecordedDirectories = maps.Clone(source.RecordedDirectories)
	cloned.Locations = append([]Location(nil), source.Locations...)
	cloned.Branch = ClonePointer(source.Branch)
	cloned.Repository = ClonePointer(source.Repository)
	cloned.DurationMs = ClonePointer(source.DurationMs)
	cloned.Cost = ClonePointer(source.Cost)
	cloned.UnattributedTokens = ClonePointer(source.UnattributedTokens)
	cloned.Entrypoint = ClonePointer(source.Entrypoint)
	cloned.Model = ClonePointer(source.Model)
	cloned.ObservedModels = slices.Clone(source.ObservedModels)
	cloned.ContextTokens = ClonePointer(source.ContextTokens)
	cloned.ContextWindow = ClonePointer(source.ContextWindow)
	cloned.FirstPrompt = ClonePointer(source.FirstPrompt)
	cloned.AgentError = ClonePointer(source.AgentError)
	cloned.LastEditAt = ClonePointer(source.LastEditAt)
	cloned.DeclaredGoal = ClonePointer(source.DeclaredGoal)
	cloned.Tokens = maps.Clone(source.Tokens)
	cloned.UnpricedModels = slices.Clone(source.UnpricedModels)
	cloned.Subagents = slices.Clone(source.Subagents)
	for i := range cloned.Subagents {
		cloned.Subagents[i].Model = ClonePointer(source.Subagents[i].Model)
		cloned.Subagents[i].DurationMs = ClonePointer(source.Subagents[i].DurationMs)
		cloned.Subagents[i].SpawnedAtTurn = ClonePointer(source.Subagents[i].SpawnedAtTurn)
		cloned.Subagents[i].UnattributedTokens = ClonePointer(source.Subagents[i].UnattributedTokens)
		cloned.Subagents[i].Cost = ClonePointer(source.Subagents[i].Cost)
		cloned.Subagents[i].Commands = slices.Clone(source.Subagents[i].Commands)
		for j := range cloned.Subagents[i].Commands {
			cloned.Subagents[i].Commands[j].Output = ClonePointer(cloned.Subagents[i].Commands[j].Output)
		}
		cloned.Subagents[i].Tokens = maps.Clone(source.Subagents[i].Tokens)
	}
	cloned.CommitLog = slices.Clone(source.CommitLog)
	cloned.Skills = slices.Clone(source.Skills)
	cloned.Commands = slices.Clone(source.Commands)
	cloned.CommandOutputs = slices.Clone(source.CommandOutputs)
	for i := range cloned.CommandOutputs {
		cloned.CommandOutputs[i] = ClonePointer(cloned.CommandOutputs[i])
	}
	cloned.Commits = slices.Clone(source.Commits)
	cloned.CommitSHAs = slices.Clone(source.CommitSHAs)
	cloned.PullRequestLinks = slices.Clone(source.PullRequestLinks)
	cloned.Todos = slices.Clone(source.Todos)
	cloned.Digest = slices.Clone(source.Digest)
	for i := range cloned.Digest {
		row := &cloned.Digest[i]
		row.Inherited = ClonePointer(row.Inherited)
		row.Active = ClonePointer(row.Active)
		row.ContextSelected = ClonePointer(row.ContextSelected)
		row.ContextDescription = ClonePointer(row.ContextDescription)
	}
	cloned.FileEdits = slices.Clone(source.FileEdits)
	for i := range cloned.FileEdits {
		cloned.FileEdits[i].ChangeIDs = slices.Clone(source.FileEdits[i].ChangeIDs)
		cloned.FileEdits[i].changes = slices.Clone(source.FileEdits[i].changes)
	}
	if source.Git != nil {
		value := *source.Git
		cloned.Git = &value
	}
	if source.Synthesis != nil {
		value := *source.Synthesis
		value.Goals = slices.Clone(source.Synthesis.Goals)
		value.KeyDecisions = slices.Clone(source.Synthesis.KeyDecisions)
		value.Decisions = slices.Clone(source.Synthesis.Decisions)
		for i := range value.Decisions {
			value.Decisions[i].Evidence = slices.Clone(source.Synthesis.Decisions[i].Evidence)
			value.Decisions[i].From = slices.Clone(source.Synthesis.Decisions[i].From)
		}
		cloned.Synthesis = &value
	}
	return &cloned
}

// ClonePointer returns a copy of *value in a new pointer, or nil for nil.
func ClonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
