package cursor

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestCommitObservationsRequireCompletedCommitCommand(t *testing.T) {
	const before = "1111111111111111111111111111111111111111"
	const after = "2222222222222222222222222222222222222222"
	checkpointOnly := `{"gitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + before + `"}}},"afterGitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + after + `"}}},"toolFormerData":{"name":"edit_file","status":"completed"}}`
	if got := commitObservationsFromIDEBubble(checkpointOnly); len(got) != 0 {
		t.Fatalf("checkpoint-only observations = %v, want none", got)
	}

	commit := `{"gitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + before + `"}}},"afterGitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + after + `"}}},"toolFormerData":{"name":"run_terminal_cmd","status":"completed","rawArgs":"{\"command\":\"git commit -m 'ship it'\"}","result":"[main 2222222] ship it"}}`
	got := commitObservationsFromIDEBubble(commit)
	if len(got) != 1 || got[0].Hash != after {
		t.Fatalf("commit observations = %v, want hash %s", got, after)
	}

	checkout := `{"gitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"` + before + `"}}},"afterGitCheckpoint":{"commitHashesByGitWorkspace":{"repo":{"commitHash":"3333333333333333333333333333333333333333"}}},"toolFormerData":{"name":"run_terminal_cmd","status":"completed","rawArgs":"{\"command\":\"git commit -m temp && git checkout other\"}","result":"[main 2222222] temp"}}`
	if got := commitObservationsFromIDEBubble(checkout); len(got) != 0 {
		t.Fatalf("compound command observations = %v, want none", got)
	}
}

func TestLoadMetadataForSessionsReturnsOnlyRequestedIDs(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	target := "00000000-0000-4000-8000-000000000001"
	other := "00000000-0000-4000-8000-000000000002"
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value, createdAt, lastUpdatedAt) VALUES
		(?, '{"name":"Target"}', 10, 20), (?, '{"name":"Other"}', 30, 40)`, target, other); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadata, err := loadMetadataForSessions(home, []string{target})
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Lookup(target); got == nil || got.Name != "Target" || got.LastActivityAt != 20 {
		t.Fatalf("target metadata = %#v", got)
	}
	if got := metadata.Lookup(other); got != nil {
		t.Fatalf("unrequested metadata = %#v, want nil", got)
	}
}

func TestLoadSelectionMetadataReadsOnlySelectionSignals(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-000000000001"
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value, createdAt, lastUpdatedAt) VALUES (?, '{}', 10, 20)`, id); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`,
		"bubbleId:"+id+":1", `{"createdAt":100,"modelInfo":{"modelName":"gpt-5"}}`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadata, err := loadSelectionMetadata(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Lookup(id); got == nil || got.LastActivityAt != 20 || got.Model != "" {
		t.Fatalf("selection metadata = %#v, want activity without full enrichment", got)
	}
}

func TestApplyCursorLivenessSupportsSelectionWithoutPollutingScopedMetadata(t *testing.T) {
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("requested").Entrypoint = entrypointIDE
	metadata.Session("ambiguous")
	applyCursorLiveness(metadata, map[string]string{"requested": entrypointIDE, "unrelated": entrypointCLI}, false)
	if metadata.Lookup("requested").Live != "interactive" || metadata.Lookup("unrelated") != nil {
		t.Fatalf("scoped liveness = %#v", metadata.Sessions)
	}
	applyCursorLiveness(metadata, map[string]string{"ambiguous": entrypointCLI}, false)
	if got := metadata.Lookup("ambiguous").Live; got != "" {
		t.Fatalf("ambiguous liveness = %q, want empty", got)
	}
	applyCursorLiveness(metadata, map[string]string{"live-cli": entrypointCLI}, true)
	if got := metadata.Lookup("live-cli"); got == nil || got.Live != "interactive" || got.Entrypoint != entrypointCLI {
		t.Fatalf("selection liveness = %#v", got)
	}
}

func TestLoadRelationshipMetadataIncludesCursorCLIParent(t *testing.T) {
	home := t.TempDir()
	parentID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	path := filepath.Join(home, ".cursor", "chats", "one", childID, "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (key TEXT, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	value := hex.EncodeToString([]byte(`{"agentId":"` + childID + `","subagentInfo":{"parentAgentId":"` + parentID + `","toolCallId":"call-1"}}`))
	if _, err := db.Exec(`INSERT INTO meta VALUES ('0', ?)`, value); err != nil {
		t.Fatal(err)
	}
	db.Close()
	metadata, err := loadRelationshipMetadataForSessions(home, []string{childID})
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Lookup(childID); got == nil || got.Relationship.ParentID != parentID {
		t.Fatalf("CLI relationship = %#v", got)
	}
}

func TestLoadRelationshipMetadataReadsOnlyRelationships(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	parentID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	header := `{"name":"Child","subagentInfo":{"parentComposerId":"` + parentID + `"}}`
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value, createdAt, lastUpdatedAt) VALUES (?, ?, 10, 20)`, childID, header); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadata, err := loadRelationshipMetadataForSessions(home, []string{childID})
	if err != nil {
		t.Fatal(err)
	}
	got := metadata.Lookup(childID)
	if got == nil || got.Relationship.ParentID != parentID {
		t.Fatalf("relationship metadata = %#v, want parent %s", got, parentID)
	}
	if got.Name != "" || got.StartedAt != 0 || got.LastActivityAt != 0 || got.Live != "" {
		t.Fatalf("relationship metadata included unrelated enrichment: %#v", got)
	}
}

func TestLoadMetadataForSessionsCanonicalizesStoredIDs(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	id := "abcdefab-cdef-4abc-8def-abcdefabcdef"
	upperID := strings.ToUpper(id)
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value, createdAt, lastUpdatedAt) VALUES (?, '{"name":"Uppercase","workspaceIdentifier":{"uri":{"fsPath":"/tmp/project"}}}', 10, 20)`, upperID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`,
		"bubbleId:"+upperID+":1", `{"createdAt":100,"modelInfo":{"modelName":"gpt-5"}}`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadata, err := loadMetadataForSessions(home, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Lookup(id); got == nil || got.Name != "Uppercase" || got.Model != "gpt-5" || got.WorkingDirectory != "/tmp/project" || got.LastActivityAt != 20 {
		t.Fatalf("canonical metadata = %#v", got)
	}
	if got := metadata.Lookup(upperID); got != nil {
		t.Fatalf("uppercase metadata key = %#v, want nil", got)
	}
}

func TestLoadIDEModelsOrdersNumericTimestampsDeterministically(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	const id = "00000000-0000-4000-8000-000000000001"
	for _, row := range []struct{ key, value string }{
		{"bubbleId:" + id + ":z", `{"createdAt":200,"modelInfo":{"modelName":"gpt-5"}}`},
		{"bubbleId:" + id + ":a", `{"createdAt":200,"modelInfo":{"modelName":"gpt-4"}}`},
		{"bubbleId:" + id + ":old", `{"createdAt":100,"modelInfo":{"modelName":"gpt-3"}}`},
	} {
		if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`, row.key, row.value); err != nil {
			t.Fatal(err)
		}
	}

	metadata := vendors.EmptySessionMetadata()
	loadIDEModelsDB(metadata, nil, db, nil)
	if got := metadata.Session(id).Model; got != "gpt-5" {
		t.Fatalf("model = %q, want newest model with deterministic key tie-break", got)
	}
}

func TestLoadIDEModelsCountsOnlyCreatedPullRequests(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	const id = "00000000-0000-4000-8000-000000000001"
	for _, row := range []struct{ key, value string }{
		{"bubbleId:" + id + ":view", `{"toolFormerData":{"name":"run_terminal_cmd","status":"completed","rawArgs":"{\"command\":\"gh pr view\"}","result":"https://github.com/centauri-ai/coslash/pull/123"}}`},
		{"bubbleId:" + id + ":create", `{"toolFormerData":{"name":"run_terminal_cmd","status":"completed","rawArgs":"{\"command\":\"gh pr create\"}","result":"https://github.com/centauri-ai/coslash/pull/124"}}`},
	} {
		if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`, row.key, row.value); err != nil {
			t.Fatal(err)
		}
	}

	metadata := vendors.EmptySessionMetadata()
	loadIDEModelsDB(metadata, nil, db, nil)
	if got := metadata.Session(id).PullRequests; got != 1 {
		t.Fatalf("pull requests = %d, want only the created pull request", got)
	}
}

func TestLoadIDEModelsBatchesLargeSelections(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprintf("00000000-0000-4000-8000-%012x", i)
	}
	target := ids[len(ids)-1]
	if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`,
		"bubbleId:"+target+":1", `{"modelInfo":{"modelName":"gpt-5"}}`); err != nil {
		t.Fatal(err)
	}

	metadata := vendors.EmptySessionMetadata()
	loadIDEModelsDB(metadata, nil, db, ids)
	if got := metadata.Session(target).Model; got != "gpt-5" {
		t.Fatalf("model = %q, want model from the final query batch", got)
	}
}

func TestLoadMetadataForSessionsExpandsIDEFamily(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	parentID := "00000000-0000-4000-8000-000000000001"
	child1ID := "00000000-0000-4000-8000-000000000002"
	child2ID := "00000000-0000-4000-8000-000000000003"
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, value string }{
		{parentID, `{"name":"Parent"}`},
		{child1ID, `{"name":"Child one","subagentInfo":{"parentComposerId":"` + parentID + `","toolCallId":"call-1"}}`},
		{child2ID, `{"name":"Child two","subagentInfo":{"parentComposerId":"` + parentID + `","toolCallId":"call-2"}}`},
		{"00000000-0000-4000-8000-000000000004", `not json`},
	} {
		if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value) VALUES (?, ?)`, row.id, row.value); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadata, err := loadMetadataForSessions(home, []string{child1ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parentID, child1ID, child2ID} {
		if metadata.Lookup(id) == nil {
			t.Fatalf("family member %s missing from scoped metadata", id)
		}
	}
}

func TestLoadIDERelationshipsResolvesRunningTaskWithoutResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.vscdb")
	if err := createMetadataTestDB(path); err != nil {
		t.Fatal(err)
	}
	parentID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	header := `{"subagentInfo":{"parentComposerId":"` + parentID + `","toolCallId":"call-1"}}`
	bubble := `{"toolFormerData":{"name":"task_v2","toolCallId":"call-1","status":"running","params":"{\"description\":\"inspect\"}","result":""}}`
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value) VALUES (?, ?)`, childID, header); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, ?)`, "bubbleId:"+parentID+":bubble-1", bubble); err != nil {
		t.Fatal(err)
	}

	metadata := vendors.EmptySessionMetadata()
	loadIDERelationships(metadata, db, nil)
	relationship := metadata.Session(childID).Relationship
	if !relationship.Active || relationship.ParentID != parentID || relationship.SpawnKey != "call-1" || relationship.Task != "inspect" {
		t.Fatalf("relationship = %#v, want active child resolved from header join", relationship)
	}
}

func createMetadataTestDB(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE composerHeaders (
		composerId TEXT PRIMARY KEY,
		value TEXT,
		createdAt INTEGER,
		lastUpdatedAt INTEGER
	); CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`)
	return err
}

func TestLoadIDEModelsKeepsContextSeparateFromCumulativeTokens(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cursorDiskKV (key TEXT, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	const id = "01234567-89ab-4def-8123-456789abcdef"
	if _, err := db.Exec(`INSERT INTO cursorDiskKV VALUES (?, ?), (?, ?)`,
		"bubbleId:"+id+":1", `{"createdAt":"2026-09-15T00:00:00Z","modelInfo":{"modelName":"claude-4.6-opus-high-thinking"}}`,
		"composerData:"+id, `{"contextTokensUsed":48200,"contextTokenLimit":256000,"latestConversationSummary":{"summary":{"summary":"  Work completed before compaction.  "}}}`,
	); err != nil {
		t.Fatal(err)
	}

	metadata := vendors.EmptySessionMetadata()
	loadIDEModelsDB(metadata, nil, db, nil)
	usage := metadata.Session(id).Usage
	if len(usage.Tokens) != 0 {
		t.Fatalf("cumulative tokens = %#v, want unavailable", usage.Tokens)
	}
	if usage.ContextTokens == nil || *usage.ContextTokens != 48200 {
		t.Fatalf("context tokens = %v, want 48200", usage.ContextTokens)
	}
	if usage.ContextWindow == nil || *usage.ContextWindow != 256000 {
		t.Fatalf("context window = %v, want 256000", usage.ContextWindow)
	}
	if got := metadata.Session(id).CompactionSeed; got != "Work completed before compaction." {
		t.Fatalf("compaction seed = %q, want stored conversation summary", got)
	}
	parsed := &vendors.ParsedSession{Session: &session.Session{ID: id}}
	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, metadata)
	if got := parsed.Session.CompactionSeed; got != "Work completed before compaction." {
		t.Fatalf("applied compaction seed = %q, want stored conversation summary", got)
	}
	if input := synthesis.BuildInput(parsed.Session); !strings.Contains(input, "COMPACTION SEED\nWork completed before compaction.") {
		t.Fatalf("synthesis input omitted compaction seed: %s", input)
	}
}

func TestLoadMetadataTreatsComposerDataAsIDELane(t *testing.T) {
	home := t.TempDir()
	const id = "01234567-89ab-4def-8123-456789abcdef"
	statePath := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDB, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE composerHeaders (composerId TEXT, value TEXT)`,
		`CREATE TABLE cursorDiskKV (key TEXT, value TEXT)`,
	} {
		if _, err := stateDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stateDB.Exec(`INSERT INTO cursorDiskKV VALUES (?, ?)`,
		"composerData:"+id, `{"latestConversationSummary":{"summary":{"summary":"IDE-only seed"}}}`,
	); err != nil {
		t.Fatal(err)
	}
	if err := stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	metadata, err := loadMetadata(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Session(id).CompactionSeed; got != "IDE-only seed" {
		t.Fatalf("IDE compaction seed = %q, want fixture seed", got)
	}
	if got := metadata.Session(id).Entrypoint; got != entrypointIDE {
		t.Fatalf("entrypoint = %q, want composer data to register IDE lane", got)
	}

	chatPath := filepath.Join(home, ".cursor", "chats", "one", "two", "store.db")
	if err := os.MkdirAll(filepath.Dir(chatPath), 0o755); err != nil {
		t.Fatal(err)
	}
	chatDB, err := sql.Open("sqlite", chatPath)
	if err != nil {
		t.Fatal(err)
	}
	cliMetadata := hex.EncodeToString([]byte(`{"agentId":"` + id + `","name":"CLI session"}`))
	if _, err := chatDB.Exec(`CREATE TABLE meta (key TEXT, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := chatDB.Exec(`INSERT INTO meta VALUES ('0', ?)`, cliMetadata); err != nil {
		t.Fatal(err)
	}
	if err := chatDB.Close(); err != nil {
		t.Fatal(err)
	}

	metadata, err = loadMetadata(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Session(id).CompactionSeed; got != "" {
		t.Fatalf("ambiguous compaction seed = %q, want empty", got)
	}
}

func TestMalformedComposerDataStillRegistersIDELane(t *testing.T) {
	home := t.TempDir()
	const id = "01234567-89ab-4def-8123-456789abcdef"
	statePath := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	stateDB, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stateDB.Exec(`INSERT INTO cursorDiskKV(key, value) VALUES (?, 'not-json')`, "composerData:"+id); err != nil {
		t.Fatal(err)
	}
	stateDB.Close()
	chatPath := filepath.Join(home, ".cursor", "chats", "one", id, "store.db")
	if err := os.MkdirAll(filepath.Dir(chatPath), 0o755); err != nil {
		t.Fatal(err)
	}
	chatDB, err := sql.Open("sqlite", chatPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chatDB.Exec(`CREATE TABLE meta (key TEXT, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	value := hex.EncodeToString([]byte(`{"agentId":"` + id + `"}`))
	if _, err := chatDB.Exec(`INSERT INTO meta VALUES ('0', ?)`, value); err != nil {
		t.Fatal(err)
	}
	chatDB.Close()
	metadata, err := loadMetadata(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Lookup(id); got == nil || got.Entrypoint != "" {
		t.Fatalf("ambiguous malformed composer metadata = %#v", got)
	}
}
