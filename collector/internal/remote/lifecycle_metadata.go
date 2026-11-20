package remote

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/atomicfile"
)

// FileMetadataSequenceStore persists the highest authenticated metadata
// sequence. The containing directory and file are private to the local user;
// replacement is atomic so interruption cannot lower the remembered value.
type FileMetadataSequenceStore struct {
	Path string
	mu   sync.Mutex
}

func (store *FileMetadataSequenceStore) Accept(sequence uint64) error {
	if store == nil || store.Path == "" || sequence == 0 {
		return ErrHelperMetadataRollback
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	highest, err := readMetadataSequence(store.Path)
	if err != nil {
		return err
	}
	if sequence < highest {
		return ErrHelperMetadataRollback
	}
	if sequence == highest {
		return nil
	}
	directory := filepath.Dir(store.Path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create metadata sequence directory: %w", err)
	}
	if err := protectMetadataSequenceDirectory(directory); err != nil {
		return fmt.Errorf("secure metadata sequence directory: %w", err)
	}
	if err := atomicfile.Write(store.Path, []byte(strconv.FormatUint(sequence, 10)+"\n"), 0o600, protectMetadataSequenceFile); err != nil {
		return fmt.Errorf("commit metadata sequence: %w", err)
	}
	// Write syncs the directory only on a best-effort basis; a lost sequence
	// would let a rolled-back helper pass, so this sync must succeed.
	return atomicfile.SyncDir(directory)
}

func readMetadataSequence(path string) (uint64, error) {
	content, err := readMetadataSequenceContent(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read metadata sequence: %w", err)
	}
	if len(content) > 32 {
		return 0, fmt.Errorf("%w: invalid local sequence state", ErrHelperMetadata)
	}
	sequence, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
	if err != nil || sequence == 0 {
		return 0, fmt.Errorf("%w: invalid local sequence state", ErrHelperMetadata)
	}
	return sequence, nil
}
