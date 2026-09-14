package cursor

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	_ "modernc.org/sqlite"
)

func TestLoadIDEModelsKeepsContextSeparateFromTokenUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE cursorDiskKV (key TEXT, value TEXT)")
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	id := "agent-123e4567-e89b-12d3-a456-426614174000"
	value, err := json.Marshal(map[string]any{
		"contextTokensUsed": 1234,
		"contextTokenLimit": 8192,
		"usageData": map[string]any{
			"claude": map[string]any{"costInCents": 0.0},
		},
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO cursorDiskKV(key, value) VALUES(?, ?)", "composerData:"+id, value); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadata := vendors.EmptySessionMetadata()
	loadIDEModels(metadata, path)
	usage := metadata.Session(id).Usage
	if len(usage.Tokens) != 0 {
		t.Fatalf("context usage fabricated token buckets: %#v", usage.Tokens)
	}
	if usage.ContextTokens == nil || *usage.ContextTokens != 1234 {
		t.Fatalf("context tokens = %v, want 1234", usage.ContextTokens)
	}
	if usage.ContextWindow == nil || *usage.ContextWindow != 8192 {
		t.Fatalf("context window = %v, want 8192", usage.ContextWindow)
	}
	if usage.RecordedCost == nil || *usage.RecordedCost != 0 {
		t.Fatalf("recorded cost = %v, want known zero", usage.RecordedCost)
	}
}
