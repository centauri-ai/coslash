package hubclient

import (
	"context"
	"errors"
)

var (
	ErrNotPaired                  = errors.New("hub device credential is not available")
	ErrCredentialStoreUnavailable = errors.New("secure credential store unavailable")
)

type CredentialStore interface {
	Load(context.Context) (string, error)
	Save(context.Context, string) error
	Delete(context.Context) error
}

type ConditionalCredentialDeleter interface {
	DeleteIfMatches(context.Context, string) (bool, error)
}

var osKeychainMutationMu = make(chan struct{}, 1)

func lockOSKeychainMutation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case osKeychainMutationMu <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-osKeychainMutationMu
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func unlockOSKeychainMutation() {
	<-osKeychainMutationMu
}

func (s OSKeychain) Save(ctx context.Context, credential string) error {
	if err := lockOSKeychainMutation(ctx); err != nil {
		return err
	}
	defer unlockOSKeychainMutation()
	return s.save(ctx, credential)
}

func (s OSKeychain) Delete(ctx context.Context) error {
	if err := lockOSKeychainMutation(ctx); err != nil {
		return err
	}
	defer unlockOSKeychainMutation()
	return s.delete(ctx)
}

func (s OSKeychain) DeleteIfMatches(ctx context.Context, expected string) (bool, error) {
	if expected == "" {
		return false, nil
	}
	if err := lockOSKeychainMutation(ctx); err != nil {
		return false, err
	}
	defer unlockOSKeychainMutation()
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
