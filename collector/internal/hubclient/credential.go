package hubclient

import (
	"context"
	"errors"
	"sync"
)

var ErrNotPaired = errors.New("hub device credential is not available")

type CredentialStore interface {
	Load(context.Context) (string, error)
	Save(context.Context, string) error
}

type ConditionalCredentialDeleter interface {
	DeleteIfMatches(context.Context, string) (bool, error)
}

var osKeychainMutationMu sync.Mutex

func (s OSKeychain) Save(ctx context.Context, credential string) error {
	osKeychainMutationMu.Lock()
	defer osKeychainMutationMu.Unlock()
	return s.save(ctx, credential)
}

func (s OSKeychain) Delete(ctx context.Context) error {
	osKeychainMutationMu.Lock()
	defer osKeychainMutationMu.Unlock()
	return s.delete(ctx)
}

func (s OSKeychain) DeleteIfMatches(ctx context.Context, expected string) (bool, error) {
	if expected == "" {
		return false, nil
	}
	osKeychainMutationMu.Lock()
	defer osKeychainMutationMu.Unlock()
	current, err := s.Load(ctx)
	if errors.Is(err, ErrNotPaired) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current != expected {
		return false, nil
	}
	if err := s.delete(ctx); err != nil {
		return false, err
	}
	return true, nil
}

type OSKeychain struct {
	Service string
	Account string
}
