package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

type claudeTestParseCache struct {
	entries map[vendors.CacheKey]json.RawMessage
	stores  int
}

func (cache *claudeTestParseCache) Lookup(key vendors.CacheKey) (json.RawMessage, bool) {
	payload, ok := cache.entries[key]
	return payload, ok
}

func (cache *claudeTestParseCache) Store(key vendors.CacheKey, payload json.RawMessage) error {
	cache.entries[key] = append(json.RawMessage(nil), payload...)
	cache.stores++
	return nil
}

func TestParseCacheDoesNotStoreOutputFromToolResultsFile(t *testing.T) {
	projects := t.TempDir()
	transcript := filepath.Join(projects, persistedRoot+".jsonl")
	outputFile := filepath.Join(projects, persistedRoot, "tool-results", "big.txt")
	if err := os.MkdirAll(filepath.Dir(outputFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputFile, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	persistedTranscript(t, transcript, map[string]string{
		"a": wrapper(persistedHome+persistedRoot+"/tool-results/big.txt", "preview"),
	})

	cache := &claudeTestParseCache{entries: map[vendors.CacheKey]json.RawMessage{}}
	vendors.SetParseCache(cache)
	t.Cleanup(func() { vendors.SetParseCache(nil) })
	parseOutput := func() string {
		t.Helper()
		parsed, err := parseSourceCachedContext(t.Context(), vendors.LocalReadSource, transcript)
		if err != nil || len(parsed.transcript.Session.CommandOutputs) != 1 || parsed.transcript.Session.CommandOutputs[0] == nil {
			t.Fatalf("parsed command output = %#v, %v", parsed, err)
		}
		return *parsed.transcript.Session.CommandOutputs[0]
	}
	if got := parseOutput(); got != "first" {
		t.Fatalf("initial output = %q, want first", got)
	}
	if cache.stores != 0 {
		t.Fatalf("cache stores = %d, want 0 for file-backed output", cache.stores)
	}
	if err := os.WriteFile(outputFile, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := parseOutput(); got != "second" {
		t.Fatalf("changed output = %q, want second", got)
	}
	if err := os.Remove(outputFile); err != nil {
		t.Fatal(err)
	}
	if got := parseOutput(); got != "preview\n..." {
		t.Fatalf("missing output fallback = %q, want preview", got)
	}
}
