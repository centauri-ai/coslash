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
	"Name":                {true, "bounded session.name"},
	"Summary":             {true, "bounded session.summary"},
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
	"Subagents":           {true, "bounded session.subagents facts"},
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
	"FirstPrompt":      {true, "bounded session.firstPrompt"},
	"Commands":         {false, "raw commands never cross; only len() as session.counts.commands"},
	"Commits":          {true, "bounded commit subjects"},
	"PullRequests":     {true, "session.counts.pullRequests"},
	"Todos":            {true, "bounded session.todos"},
	"Digest":           {true, "bounded session.digest"},
	"FileEdits":        {true, "session.fileEdits statistics with repository-relative paths"},
	"Git":              {true, "session.git drift counts"},
	"GitProbed":        {false, "local probe state"},
	"LastEditAt":       {true, "session.lastEditAtMs"},
	"Synthesis":        {false, "local synthesis output stays on the machine"},
	"SynthesisPending": {false, "local synthesis state"},
	"DeclaredGoal":     {true, "bounded session.declaredGoal"},
	"CompactionSeed":   {false, "local parser state"},
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
