package remote

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileMetadataSequenceStorePersistsHighWaterMark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "sequence")
	store := &FileMetadataSequenceStore{Path: path}
	if err := store.Accept(4); err != nil {
		t.Fatal(err)
	}
	if err := (&FileMetadataSequenceStore{Path: path}).Accept(4); err != nil {
		t.Fatalf("same sequence was not idempotent: %v", err)
	}
	if err := (&FileMetadataSequenceStore{Path: path}).Accept(3); !errors.Is(err, ErrHelperMetadataRollback) {
		t.Fatalf("rollback error = %v", err)
	}
	if err := (&FileMetadataSequenceStore{Path: path}).Accept(5); err != nil {
		t.Fatal(err)
	}
}

type MemoryMetadataSequenceStore struct {
	mu      sync.Mutex
	highest uint64
}

func (store *MemoryMetadataSequenceStore) Accept(sequence uint64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if sequence < store.highest {
		return ErrHelperMetadataRollback
	}
	store.highest = sequence
	return nil
}
