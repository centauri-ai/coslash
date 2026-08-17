package sessionexport

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

func TestMarshalDeterministicallyFitsHeavySession(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	todoText := strings.Repeat("t", maxTodoTextBytes)
	label := strings.Repeat("l", maxCommandLabelBytes+1)
	task := strings.Repeat("q", maxSubagentTextBytes)
	result := strings.Repeat("r", maxSubagentTextBytes)
	commands := make([]session.SubagentCommand, maxCommandLabelItems)
	for i := range commands {
		commands[i] = session.SubagentCommand{Label: label, Command: fmt.Sprintf("raw-%d", i)}
	}
	todos := make([]session.Todo, 80)
	for i := range todos {
		todos[i] = session.Todo{Text: todoText}
	}
	local := session.Session{
		Agent: "codex", ID: "heavy", Repository: &repository, StartedAt: 1,
		Tokens: map[string]session.ModelTokens{},
		Subagents: []session.Subagent{{
			ID: "child", Name: "reviewer", Status: session.SubagentReturned,
			Task: task, Result: result, Commands: commands, Tokens: map[string]session.ModelTokens{},
		}},
		SessionDetails: session.SessionDetails{Todos: todos},
	}

	built, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshotv1.Size(built)
	if err != nil {
		t.Fatal(err)
	}
	fastSize, err := encodedSnapshotSize(built)
	if err != nil {
		t.Fatal(err)
	}
	if fastSize != before {
		t.Fatalf("fast encoded size = %d; canonical size = %d", fastSize, before)
	}
	if before <= snapshotv1.MaxPayloadBytes {
		t.Fatalf("test profile is only %d bytes; want over %d", before, snapshotv1.MaxPayloadBytes)
	}

	first, err := Marshal(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("aggregate fitting is not deterministic")
	}
	if len(first) > snapshotv1.MaxPayloadBytes {
		t.Fatalf("fitted snapshot is %d bytes", len(first))
	}
	decoded, err := snapshotv1.Decode(first)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(decoded.Session.Subagents[0].CommandLabels); got >= len(commands) {
		t.Fatalf("command labels were not reduced: %d", got)
	}
	if decoded.Session.Subagents[0].Result != result || decoded.Session.Subagents[0].Task != task {
		t.Fatal("later degradation stage ran before command labels were sufficient")
	}
	if len(decoded.Session.Todos) != len(todos) {
		t.Fatal("todos changed before higher-priority degradation was exhausted")
	}
	if !hasAggregateTruncation(decoded.Truncation, "/session/subagents/0/commandLabels") {
		t.Fatalf("aggregate reduction was not recorded: %#v", decoded.Truncation)
	}
}

func TestMarshalFailsWhenMandatoryCoreCannotFit(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	models := make(map[string]session.ModelTokens, snapshotv1.MaxUsageModels)
	for i := 0; i < snapshotv1.MaxUsageModels; i++ {
		models[fmt.Sprintf("model-%03d", i)] = session.ModelTokens{InputTokens: 1}
	}
	subagents := make([]session.Subagent, maxSubagentItems)
	for i := range subagents {
		subagents[i] = session.Subagent{
			ID: fmt.Sprintf("child-%03d", i), Name: "worker", Status: session.SubagentReturned,
			Commands: []session.SubagentCommand{}, Tokens: models,
		}
	}
	local := session.Session{
		Agent: "codex", ID: "irreducible", Repository: &repository, StartedAt: 1,
		Tokens: map[string]session.ModelTokens{}, Subagents: subagents,
	}
	_, err := Marshal(local, BuildOptions{CollectorVersion: "0.1.0"})
	if !errors.Is(err, snapshotv1.ErrOversized) {
		t.Fatalf("mandatory overflow error = %v", err)
	}
}

func TestMarshalReducesOptionalSessionMetadataBeforeMandatoryOverflow(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	models := make(map[string]session.ModelTokens, 40)
	for i := range 40 {
		prefix := fmt.Sprintf("model-%02d-", i)
		models[prefix+strings.Repeat("m", maxModelBytes-len(prefix))] = session.ModelTokens{InputTokens: 1}
	}

	local := aggregateLocal(repository)
	for i := 0; i < maxSubagentItems; i++ {
		local.Subagents = append(local.Subagents, session.Subagent{
			ID: fmt.Sprintf("child-%03d", i), Name: "worker", Status: session.SubagentReturned,
			Commands: []session.SubagentCommand{}, Tokens: models,
		})
		built, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
		if err != nil {
			t.Fatal(err)
		}
		size, err := snapshotv1.Size(built)
		if err != nil {
			t.Fatal(err)
		}
		if size > snapshotv1.MaxPayloadBytes {
			local.Subagents = local.Subagents[:len(local.Subagents)-1]
			break
		}
	}
	if len(local.Subagents) == 0 {
		t.Fatal("could not construct a fitting mandatory core")
	}

	summary := strings.Repeat("s", maxSummaryBytes)
	goal := strings.Repeat("g", maxGoalBytes)
	prompt := strings.Repeat("p", maxPromptBytes)
	local.Summary = &summary
	local.DeclaredGoal = &goal
	local.FirstPrompt = &prompt
	built, err := Build(local, BuildOptions{CollectorVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshotv1.Size(built)
	if err != nil {
		t.Fatal(err)
	}
	if before <= snapshotv1.MaxPayloadBytes {
		t.Fatalf("optional metadata profile is only %d bytes; want over %d", before, snapshotv1.MaxPayloadBytes)
	}

	decoded := marshalAggregate(t, local)
	if len(decoded.Session.Subagents) != len(local.Subagents) {
		t.Fatal("mandatory subagent usage was reduced")
	}
	for _, item := range decoded.Truncation {
		if item.Path == "/session" && item.Reason == snapshotv1.TruncationReasonAggregateBudget && item.OriginalItems != nil && *item.OriginalItems == 3 && item.ExportedItems != nil && *item.ExportedItems < 3 {
			return
		}
	}
	t.Fatalf("optional session metadata reduction was not recorded: %#v", decoded.Truncation)
}

func TestMarshalRetainsMostRecentlyTouchedFileDuringAggregateFitting(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	edits := session.NewFileEditSet()
	edits.Add("hot.go", 1, 0, false)
	for i := 0; i < 300; i++ {
		prefix := fmt.Sprintf("cold-%03d-", i)
		edits.Add(prefix+strings.Repeat("p", maxPathBytes-len(prefix)), 1, 0, false)
	}
	edits.Add("hot.go", 1, 1, false)

	local := aggregateLocal(repository)
	local.WorkingDirectory = "/repo"
	local.FileEdits = edits.Edits
	decoded := marshalAggregateWithOptions(t, local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: "/repo"})
	if len(decoded.Session.FileEdits) >= len(local.FileEdits) {
		t.Fatal("test profile did not require file-edit fitting")
	}
	for _, edit := range decoded.Session.FileEdits {
		if edit.Path == "hot.go" {
			if edit.Edits != 2 || edit.Additions != 2 || edit.Deletions != 1 {
				t.Fatalf("retained hot edit = %#v", edit)
			}
			return
		}
	}
	t.Fatal("most recently touched file was removed as old evidence")
}

func TestMarshalAppliesAggregateReductionStages(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"

	t.Run("subagent prose", func(t *testing.T) {
		local := aggregateLocal(repository)
		local.Todos = aggregateTodos(124)
		local.Subagents = []session.Subagent{{
			ID: "child", Name: "worker", Status: session.SubagentReturned,
			Task: strings.Repeat("t", maxSubagentTextBytes), Result: strings.Repeat("r", maxSubagentTextBytes),
			Tokens: map[string]session.ModelTokens{},
		}}
		decoded := marshalAggregate(t, local)
		if decoded.Session.Subagents[0].Result != "" || decoded.Session.Subagents[0].Task == local.Subagents[0].Task || len(decoded.Session.Todos) != len(local.Todos) {
			t.Fatal("subagent result and task were not reduced before todos")
		}
		assertFirstAggregatePath(t, decoded, "/session/subagents/0/result")
	})

	t.Run("digest answers", func(t *testing.T) {
		local := aggregateLocal(repository)
		local.Todos = aggregateTodos(115)
		local.Digest = make([]session.DigestEntry, 10)
		for i := range local.Digest {
			local.Digest[i] = session.DigestEntry{Turn: i, Category: session.DigestUser, Description: "d", Answer: strings.Repeat("a", maxDigestTextBytes)}
		}
		decoded := marshalAggregate(t, local)
		if decoded.Session.Digest[0].Answer != nil || len(decoded.Session.Digest) != len(local.Digest) || len(decoded.Session.Todos) != len(local.Todos) {
			t.Fatal("digest answers were not reduced before digest entries or todos")
		}
		assertFirstAggregatePath(t, decoded, "/session/digest")
	})

	t.Run("older digest", func(t *testing.T) {
		local := aggregateLocal(repository)
		local.Digest = make([]session.DigestEntry, 80)
		for i := range local.Digest {
			local.Digest[i] = session.DigestEntry{Turn: i, Category: session.DigestUser, Description: strings.Repeat("d", maxDigestTextBytes)}
		}
		decoded := marshalAggregate(t, local)
		if len(decoded.Session.Digest) >= len(local.Digest) || decoded.Session.Digest[0].Turn == 0 || decoded.Session.Digest[len(decoded.Session.Digest)-1].Turn != len(local.Digest)-1 {
			t.Fatal("older digest entries were not removed while retaining the newest")
		}
		assertFirstAggregatePath(t, decoded, "/session/digest")
	})

	t.Run("todos", func(t *testing.T) {
		local := aggregateLocal(repository)
		local.Todos = aggregateTodos(maxTodoItems)
		for i := range local.Todos {
			local.Todos[i].Text += "x"
		}
		decoded := marshalAggregate(t, local)
		if len(decoded.Session.Todos) >= len(local.Todos) {
			t.Fatal("todos were not reduced")
		}
		assertFirstAggregatePath(t, decoded, "/session/todos")
	})

	t.Run("commits", func(t *testing.T) {
		local := aggregateLocal(repository)
		local.Commits = make([]string, maxCommitItems)
		for i := range local.Commits {
			prefix := fmt.Sprintf("%03d-", i)
			local.Commits[i] = prefix + strings.Repeat("c", maxCommitTextBytes-len(prefix))
		}
		decoded := marshalAggregate(t, local)
		if len(decoded.Session.Commits) >= len(local.Commits) || decoded.Session.Commits[0] == local.Commits[0] || decoded.Session.Commits[len(decoded.Session.Commits)-1] != local.Commits[len(local.Commits)-1] {
			t.Fatal("commits were not reduced while retaining the newest")
		}
		assertFirstAggregatePath(t, decoded, "/session/commits")
	})

	t.Run("file edits", func(t *testing.T) {
		local := aggregateLocal(repository)
		local.FileEdits = make([]session.FileEdit, 300)
		for i := range local.FileEdits {
			prefix := fmt.Sprintf("%03d-", i)
			local.FileEdits[i] = session.FileEdit{Path: prefix + strings.Repeat("p", maxPathBytes-len(prefix))}
		}
		local.WorkingDirectory = "/repo"
		decoded := marshalAggregateWithOptions(t, local, BuildOptions{CollectorVersion: "0.1.0", RepositoryRoot: "/repo"})
		if len(decoded.Session.FileEdits) >= len(local.FileEdits) || decoded.Session.FileEdits[0].Path == local.FileEdits[0].Path || decoded.Session.FileEdits[len(decoded.Session.FileEdits)-1].Path != local.FileEdits[len(local.FileEdits)-1].Path {
			t.Fatal("file edits were not reduced while retaining the newest")
		}
		assertFirstAggregatePath(t, decoded, "/session/fileEdits")
	})
}

func TestMarshalRemovesMetadataForRemovedAggregateEvidence(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	local := aggregateLocal(repository)
	local.Todos = aggregateTodos(115)
	local.Digest = make([]session.DigestEntry, 10)
	for i := range local.Digest {
		local.Digest[i] = session.DigestEntry{
			Turn: i, Category: session.DigestUser, Description: "d",
			Answer: strings.Repeat("a", maxDigestTextBytes+1) + ` {"password":"digest-secret"}`,
		}
	}

	decoded := marshalAggregate(t, local)
	if decoded.Session.Digest[0].Answer != nil {
		t.Fatal("digest answers were not removed")
	}
	for i, digest := range decoded.Session.Digest {
		if digest.Answer != nil {
			continue
		}
		removedAnswer := fmt.Sprintf("/session/digest/%d/answer", i)
		for _, item := range decoded.Truncation {
			if item.Path == removedAnswer || strings.HasPrefix(item.Path, removedAnswer+"/") {
				t.Fatalf("removed answer retained truncation pointer %q", item.Path)
			}
		}
		for _, item := range decoded.Redactions {
			if item.Path == removedAnswer || strings.HasPrefix(item.Path, removedAnswer+"/") {
				t.Fatalf("removed answer retained redaction pointer %q", item.Path)
			}
		}
	}
}

func TestMarshalMergesCollectionBudgetsAndDropsRemovedItemMetadata(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	local := aggregateLocal(repository)
	local.Todos = aggregateTodos(maxTodoItems + 5)
	for i := range local.Todos {
		local.Todos[i].Text += "x"
	}

	decoded := marshalAggregate(t, local)
	collectionRecords := 0
	retainedItemRecords := 0
	for _, item := range decoded.Truncation {
		switch {
		case item.Path == "/session/todos":
			collectionRecords++
			if item.Reason != snapshotv1.TruncationReasonAggregateBudget || item.OriginalItems == nil || *item.OriginalItems != len(local.Todos) || item.ExportedItems == nil || *item.ExportedItems != len(decoded.Session.Todos) {
				t.Fatalf("merged todo budget = %#v", item)
			}
		case strings.HasPrefix(item.Path, "/session/todos/"):
			retainedItemRecords++
		}
	}
	if collectionRecords != 1 {
		t.Fatalf("todo collection budget records = %d; want 1", collectionRecords)
	}
	if retainedItemRecords != len(decoded.Session.Todos) {
		t.Fatalf("retained todo metadata = %d; want %d", retainedItemRecords, len(decoded.Session.Todos))
	}
}

func TestByteIndexForRuneCount(t *testing.T) {
	value := "a界🙂z"
	for count, want := range []string{"", "a", "a界", "a界🙂", value} {
		if got := value[:byteIndexForRuneCount(value, count)]; got != want {
			t.Fatalf("first %d runes = %q; want %q", count, got, want)
		}
	}
}

func TestMarshalReindexesMetadataWhenOlderDigestIsRemoved(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	local := aggregateLocal(repository)
	local.Digest = make([]session.DigestEntry, 80)
	for i := range local.Digest {
		local.Digest[i] = session.DigestEntry{
			Turn: i, Category: session.DigestUser,
			Description: strings.Repeat("d", maxDigestTextBytes+1),
		}
	}

	decoded := marshalAggregate(t, local)
	if len(decoded.Session.Digest) == 0 || decoded.Session.Digest[0].Turn == 0 {
		t.Fatal("older digest evidence was not removed")
	}
	if !hasMetadataPathPrefix(decoded.Truncation, "/session/digest/0/") {
		t.Fatalf("retained digest metadata was not reindexed: %#v", decoded.Truncation)
	}
}

func hasMetadataPathPrefix(values []snapshotv1.Truncation, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value.Path, prefix) {
			return true
		}
	}
	return false
}

func aggregateLocal(repository string) session.Session {
	return session.Session{
		Agent: "codex", ID: "aggregate", Repository: &repository, StartedAt: 1,
		Tokens: map[string]session.ModelTokens{},
	}
}

func aggregateTodos(count int) []session.Todo {
	values := make([]session.Todo, count)
	for i := range values {
		values[i].Text = strings.Repeat("t", maxTodoTextBytes)
	}
	return values
}

func marshalAggregate(t *testing.T, local session.Session) snapshotv1.Snapshot {
	t.Helper()
	return marshalAggregateWithOptions(t, local, BuildOptions{CollectorVersion: "0.1.0"})
}

func marshalAggregateWithOptions(t *testing.T, local session.Session, options BuildOptions) snapshotv1.Snapshot {
	t.Helper()
	data, err := Marshal(local, options)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := snapshotv1.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	assertMetadataPointersResolve(t, data, decoded)
	return decoded
}

func assertFirstAggregatePath(t *testing.T, snapshot snapshotv1.Snapshot, path string) {
	t.Helper()
	for _, item := range snapshot.Truncation {
		if item.Reason == snapshotv1.TruncationReasonAggregateBudget {
			if item.Path != path {
				t.Fatalf("first aggregate path = %q; want %q", item.Path, path)
			}
			return
		}
	}
	t.Fatalf("aggregate truncation %q not recorded", path)
}

func hasAggregateTruncation(values []snapshotv1.Truncation, path string) bool {
	for _, value := range values {
		if value.Path == path && value.Reason == snapshotv1.TruncationReasonAggregateBudget {
			return true
		}
	}
	return false
}
