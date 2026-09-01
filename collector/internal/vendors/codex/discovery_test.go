package codex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestReadHeaderSourceMarksInvalidHeaderData(t *testing.T) {
	path := filepath.Join(
		t.TempDir(),
		"rollout-2026-08-31T12-00-00-01234567-89ab-cdef-0123-456789abcdef.jsonl",
	)
	if err := os.WriteFile(path, []byte(`{"type":"event_msg"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := readHeaderSource(vendors.LocalReadSource, path)
	if !errors.Is(err, vendors.ErrInvalidData) {
		t.Fatalf("readHeaderSource error=%v, want invalid transcript data", err)
	}
}
