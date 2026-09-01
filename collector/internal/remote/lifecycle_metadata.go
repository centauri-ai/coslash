package remote

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure metadata sequence directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".helper-metadata-sequence-*")
	if err != nil {
		return fmt.Errorf("create metadata sequence temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(strconv.FormatUint(sequence, 10) + "\n"); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, store.Path); err != nil {
		return fmt.Errorf("commit metadata sequence: %w", err)
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}

func readMetadataSequence(path string) (uint64, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read metadata sequence: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || len(content) > 32 {
		return 0, fmt.Errorf("%w: invalid local sequence state", ErrHelperMetadata)
	}
	sequence, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
	if err != nil || sequence == 0 {
		return 0, fmt.Errorf("%w: invalid local sequence state", ErrHelperMetadata)
	}
	return sequence, nil
}
