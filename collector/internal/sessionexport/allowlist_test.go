package sessionexport

import (
	"reflect"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

// decision records why each local field does or does not cross the export
// boundary. A field is exported only when Build maps it explicitly.
type decision struct {
	exported bool
	reason   string
}

// census is the reviewed allow-list. Adding a field to session.Session or
// session.SessionDetails fails TestLocalSessionFieldsHaveAnExportDecision
// until it is recorded here, which keeps new local fields excluded by default.
var census = map[string]decision{
	// session.Session
	"Agent":               {true, "envelope agent"},
	"ID":                  {true, "envelope sourceSessionId"},
	"Name":                {false, "free-form session titles remain local"},
	"Summary":             {false, "free-form summaries remain local"},
	"Status":              {true, "bounded session.status"},
	"WorkingDirectory":    {true, "session.cwd, repository-relative or omitted"},
	"Branch":              {true, "bounded session.branch"},
	"Repository":          {true, "repository.canonical"},
	"RepositoryLocalOnly": {true, "repository.localOnly"},
	"EditedFileCount":     {true, "session.counts.editedFiles"},
	"DurationMs":          {true, "session.durationMs"},
	"Tokens":              {true, "session.usage.models, sorted by model"},
	"Cost":                {true, "session.usage.estimatedCostMicroUsd, frozen as integer"},
	"UnpricedModels":      {true, "session.usage.unpricedModels"},
	"Subagents":           {false, "subagent task, result, and command details remain local"},
	"StartedAt":           {true, "envelope sessionStartedAtMs"},
	"LastActivityTime":    {true, "session.lastActivityAtMs"},
	"Entrypoint":          {true, "bounded session.entrypoint"},

	// session.SessionDetails
	"Model":            {true, "session.model"},
	"ContextTokens":    {true, "session.contextTokens"},
	"ContextWindow":    {true, "session.contextWindow"},
	"Turns":            {true, "session.counts.turns"},
	"ToolUses":         {true, "session.counts.toolUses"},
	"Errors":           {true, "session.counts.errors"},
	"Compactions":      {true, "session.counts.compactions"},
	"FirstPrompt":      {false, "raw prompts remain local"},
	"Commands":         {false, "raw commands never cross; only len() as session.counts.commands"},
	"CommitLog":        {false, "local commit observations used for git reconciliation"},
	"Commits":          {false, "commit subjects can contain arbitrary local content"},
	"CommitSHAs":       {true, "resolved lowercase full Git object IDs only"},
	"PullRequests":     {true, "session.counts.pullRequests"},
	"Todos":            {false, "todo text can contain arbitrary local content"},
	"Digest":           {false, "digest text can reproduce prompt or transcript content"},
	"FileEdits":        {true, "session.fileEdits statistics with repository-relative paths"},
	"Git":              {true, "session.git drift counts"},
	"GitProbed":        {false, "local probe state"},
	"LastEditAt":       {true, "session.lastEditAtMs"},
	"Synthesis":        {false, "local synthesis output stays on the machine"},
	"SynthesisPending": {false, "local synthesis state"},
	"DeclaredGoal":     {false, "free-form goals remain local"},
	"CompactionSeed":   {false, "local parser state"},
}

var nestedCensus = map[reflect.Type]map[string]decision{
	reflect.TypeOf(session.ModelTokens{}): {
		"InputTokens":                {true, "model usage inputTokens"},
		"OutputTokens":               {true, "model usage outputTokens"},
		"CacheCreationInputTokens":   {true, "model usage cacheCreationInputTokens"},
		"CacheCreation1hInputTokens": {true, "model usage cacheCreation1hInputTokens"},
		"CacheReadInputTokens":       {true, "model usage cacheReadInputTokens"},
		"Cost":                       {true, "model usage estimatedCostMicroUsd"},
	},
	reflect.TypeOf(session.Subagent{}): {
		"ID":            {false, "subagents remain local"},
		"Name":          {false, "subagents remain local"},
		"Model":         {false, "subagents remain local"},
		"Status":        {false, "subagents remain local"},
		"Task":          {false, "subagents remain local"},
		"Result":        {false, "subagents remain local"},
		"DurationMs":    {false, "subagents remain local"},
		"SpawnedAtTurn": {false, "subagents remain local"},
		"ToolUses":      {false, "subagents remain local"},
		"Commands":      {false, "subagents remain local"},
		"Tokens":        {false, "subagents remain local"},
		"Cost":          {false, "subagents remain local"},
	},
	reflect.TypeOf(session.SubagentCommand{}): {
		"Label":   {false, "command labels can contain command-like content"},
		"Command": {false, "raw command never crosses"},
	},
	reflect.TypeOf(session.DigestEntry{}): {
		"Turn":        {false, "digest remains local"},
		"Category":    {false, "digest remains local"},
		"Description": {false, "digest remains local"},
		"Answer":      {false, "digest remains local"},
		"SubagentID":  {false, "digest remains local"},
		"Time":        {false, "local timeline timestamp"},
		"SpawnKey":    {false, "local parser linkage"},
	},
	reflect.TypeOf(session.FileEdit{}): {
		"Path":      {true, "proven repository-relative path"},
		"Additions": {true, "fileEdit.additions"},
		"Deletions": {true, "fileEdit.deletions"},
		"Edits":     {true, "fileEdit.edits"},
		"IsNew":     {true, "fileEdit.isNew"},
		"changes":   {false, "raw file changes never cross"},
	},
	reflect.TypeOf(session.GitDrift{}): {
		"BaseBranch": {true, "git.baseBranch"},
		"Ahead":      {true, "git.ahead"},
		"Behind":     {true, "git.behind"},
	},
	reflect.TypeOf(session.Todo{}): {
		"Text": {false, "todos remain local"},
		"Done": {false, "todos remain local"},
	},
}

func TestLocalSessionFieldsHaveAnExportDecision(t *testing.T) {
	seen := map[string]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := range t.NumField() {
			field := t.Field(i)
			if field.Anonymous && field.Type.Kind() == reflect.Struct {
				walk(field.Type)
				continue
			}
			seen[field.Name] = true
		}
	}
	walk(reflect.TypeOf(session.Session{}))

	for name := range seen {
		if _, ok := census[name]; !ok {
			t.Errorf("local field %q has no export decision. Record it in census as "+
				"excluded (default) or exported, and map it in Build before exporting it.", name)
		}
	}
	for name := range census {
		if !seen[name] {
			t.Errorf("census records %q, which no longer exists on the local session model", name)
		}
	}
}

func TestNestedLocalFieldsHaveAnExportDecision(t *testing.T) {
	for typ, decisions := range nestedCensus {
		seen := make(map[string]bool, typ.NumField())
		for i := range typ.NumField() {
			name := typ.Field(i).Name
			seen[name] = true
			if _, ok := decisions[name]; !ok {
				t.Errorf("local field %s.%s has no export decision", typ.Name(), name)
			}
		}
		for name := range decisions {
			if !seen[name] {
				t.Errorf("nested census records %s.%s, which no longer exists", typ.Name(), name)
			}
		}
	}
}
