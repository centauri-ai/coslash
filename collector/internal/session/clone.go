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
	cloned.Name = clonePointer(source.Name)
	cloned.Summary = clonePointer(source.Summary)
	cloned.Status = clonePointer(source.Status)
	cloned.Branch = clonePointer(source.Branch)
	cloned.Repository = clonePointer(source.Repository)
	cloned.DurationMs = clonePointer(source.DurationMs)
	cloned.Cost = clonePointer(source.Cost)
	cloned.UnattributedTokens = clonePointer(source.UnattributedTokens)
	cloned.Entrypoint = clonePointer(source.Entrypoint)
	cloned.Model = clonePointer(source.Model)
	cloned.ObservedModels = slices.Clone(source.ObservedModels)
	cloned.ContextTokens = clonePointer(source.ContextTokens)
	cloned.ContextWindow = clonePointer(source.ContextWindow)
	cloned.FirstPrompt = clonePointer(source.FirstPrompt)
	cloned.AgentError = clonePointer(source.AgentError)
	cloned.LastEditAt = clonePointer(source.LastEditAt)
	cloned.DeclaredGoal = clonePointer(source.DeclaredGoal)
	cloned.Tokens = maps.Clone(source.Tokens)
	cloned.UnpricedModels = slices.Clone(source.UnpricedModels)
	cloned.Subagents = slices.Clone(source.Subagents)
	for i := range cloned.Subagents {
		cloned.Subagents[i].Model = clonePointer(source.Subagents[i].Model)
		cloned.Subagents[i].DurationMs = clonePointer(source.Subagents[i].DurationMs)
		cloned.Subagents[i].SpawnedAtTurn = clonePointer(source.Subagents[i].SpawnedAtTurn)
		cloned.Subagents[i].UnattributedTokens = clonePointer(source.Subagents[i].UnattributedTokens)
		cloned.Subagents[i].Cost = clonePointer(source.Subagents[i].Cost)
		cloned.Subagents[i].Commands = slices.Clone(source.Subagents[i].Commands)
		for j := range cloned.Subagents[i].Commands {
			cloned.Subagents[i].Commands[j].Output = clonePointer(cloned.Subagents[i].Commands[j].Output)
		}
		cloned.Subagents[i].Tokens = maps.Clone(source.Subagents[i].Tokens)
	}
	cloned.CommitLog = slices.Clone(source.CommitLog)
	cloned.Skills = slices.Clone(source.Skills)
	cloned.Commands = slices.Clone(source.Commands)
	cloned.CommandOutputs = slices.Clone(source.CommandOutputs)
	for i := range cloned.CommandOutputs {
		cloned.CommandOutputs[i] = clonePointer(cloned.CommandOutputs[i])
	}
	cloned.Commits = slices.Clone(source.Commits)
	cloned.CommitSHAs = slices.Clone(source.CommitSHAs)
	cloned.PullRequestLinks = slices.Clone(source.PullRequestLinks)
	cloned.Todos = slices.Clone(source.Todos)
	cloned.Digest = slices.Clone(source.Digest)
	for i := range cloned.Digest {
		row := &cloned.Digest[i]
		row.Inherited = clonePointer(row.Inherited)
		row.Active = clonePointer(row.Active)
		row.ContextSelected = clonePointer(row.ContextSelected)
		row.ContextDescription = clonePointer(row.ContextDescription)
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
		cloned.Synthesis = &value
	}
	return &cloned
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
