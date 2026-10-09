package hubclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileCredentialStore keeps the device credential in a private file. Linux
// uses it because headless hosts have no Secret Service and a keyring that is
// locked after reboot would read as unpaired; see
// docs/decisions/linux-local-runtime.md.
type FileCredentialStore struct {
	Path string
}

var fileCredentialMu sync.Mutex

func (s FileCredentialStore) Load(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotPaired
	}
	if err != nil {
		return "", fmt.Errorf("load Hub credential: %w", err)
	}
	credential := strings.TrimSpace(string(data))
	if credential == "" {
		return "", ErrNotPaired
	}
	return credential, nil
}

func (s FileCredentialStore) Save(ctx context.Context, credential string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(credential) == "" {
		return errors.New("save Hub credential: empty credential")
	}
	fileCredentialMu.Lock()
	defer fileCredentialMu.Unlock()
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("save Hub credential: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("save Hub credential: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".credential-*")
	if err != nil {
		return fmt.Errorf("save Hub credential: %w", err)
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("save Hub credential: %w", err)
	}
	if _, err := temporary.WriteString(credential + "\n"); err != nil {
		temporary.Close()
		return fmt.Errorf("save Hub credential: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("save Hub credential: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("save Hub credential: %w", err)
	}
	if err := os.Rename(temporary.Name(), s.Path); err != nil {
		return fmt.Errorf("save Hub credential: %w", err)
	}
	return nil
}

func (s FileCredentialStore) Delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fileCredentialMu.Lock()
	defer fileCredentialMu.Unlock()
	return s.delete()
}

func (s FileCredentialStore) DeleteIfMatches(ctx context.Context, expected string) (bool, error) {
	if expected == "" {
		return false, nil
	}
	fileCredentialMu.Lock()
	defer fileCredentialMu.Unlock()
	current, err := s.Load(ctx)
	if errors.Is(err, ErrNotPaired) {
		return false, nil
	}
	if err != nil || current != expected {
		return false, err
	}
	if err := s.delete(); err != nil {
		return false, err
	}
	return true, nil
}

func (s FileCredentialStore) delete() error {
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete Hub credential: %w", err)
	}
	return nil
}
