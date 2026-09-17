package session

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
	cloned.Entrypoint = clonePointer(source.Entrypoint)
	cloned.Model = clonePointer(source.Model)
	cloned.ContextTokens = clonePointer(source.ContextTokens)
	cloned.ContextWindow = clonePointer(source.ContextWindow)
	cloned.FirstPrompt = clonePointer(source.FirstPrompt)
	cloned.LastEditAt = clonePointer(source.LastEditAt)
	cloned.DeclaredGoal = clonePointer(source.DeclaredGoal)
	cloned.Tokens = cloneMap(source.Tokens)
	cloned.UnpricedModels = cloneSlice(source.UnpricedModels)
	cloned.Subagents = cloneSlice(source.Subagents)
	for i := range cloned.Subagents {
		cloned.Subagents[i].Model = clonePointer(source.Subagents[i].Model)
		cloned.Subagents[i].DurationMs = clonePointer(source.Subagents[i].DurationMs)
		cloned.Subagents[i].SpawnedAtTurn = clonePointer(source.Subagents[i].SpawnedAtTurn)
		cloned.Subagents[i].Cost = clonePointer(source.Subagents[i].Cost)
		cloned.Subagents[i].Commands = cloneSlice(source.Subagents[i].Commands)
		cloned.Subagents[i].Tokens = cloneMap(source.Subagents[i].Tokens)
	}
	cloned.CommitLog = cloneSlice(source.CommitLog)
	cloned.Commands = cloneSlice(source.Commands)
	cloned.Commits = cloneSlice(source.Commits)
	cloned.CommitSHAs = cloneSlice(source.CommitSHAs)
	cloned.Todos = cloneSlice(source.Todos)
	cloned.Digest = cloneSlice(source.Digest)
	cloned.FileEdits = cloneSlice(source.FileEdits)
	for i := range cloned.FileEdits {
		cloned.FileEdits[i].changes = cloneSlice(source.FileEdits[i].changes)
	}
	if source.Git != nil {
		value := *source.Git
		cloned.Git = &value
	}
	if source.Synthesis != nil {
		value := *source.Synthesis
		value.Goals = cloneSlice(source.Synthesis.Goals)
		value.KeyDecisions = cloneSlice(source.Synthesis.KeyDecisions)
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

func cloneSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	return append([]T(nil), value...)
}

func cloneMap[K comparable, V any](value map[K]V) map[K]V {
	if value == nil {
		return nil
	}
	cloned := make(map[K]V, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}
