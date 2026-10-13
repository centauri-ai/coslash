//go:build !windows

package hubclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeychainCommandLoadErrorPreservesContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	commandErr := errors.New("exit status 1")
	if err := keychainCommandLoadError(ctx, commandErr, "could not be found"); !errors.Is(err, context.Canceled) {
		t.Fatalf("load error = %v, want %v", err, context.Canceled)
	}
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if err := keychainCommandLoadError(deadlineCtx, commandErr, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("load error = %v, want %v", err, context.DeadlineExceeded)
	}
	if err := keychainCommandLoadError(context.Background(), commandErr, "security: The specified item could not be found in the keychain."); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("load error = %v, want %v", err, ErrNotPaired)
	}
}

func TestKeychainCommandLoadErrorKeepsAccessFailuresRetryable(t *testing.T) {
	for _, output := range []string{
		"security: User interaction is not allowed.",
		"security: The specified keychain could not be found.",
		"keychain service not found",
	} {
		t.Run(output, func(t *testing.T) {
			commandErr := errors.New("exit status 36")
			err := keychainCommandLoadError(context.Background(), commandErr, output)
			if !errors.Is(err, ErrCredentialStoreUnavailable) || errors.Is(err, ErrNotPaired) || !errors.Is(err, commandErr) {
				t.Fatalf("load error = %v, want retryable store error preserving command failure", err)
			}
		})
	}
}
