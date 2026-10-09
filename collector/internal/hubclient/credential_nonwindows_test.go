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
	if err := keychainCommandLoadError(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("load error = %v, want %v", err, context.Canceled)
	}
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if err := keychainCommandLoadError(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("load error = %v, want %v", err, context.DeadlineExceeded)
	}
	if err := keychainCommandLoadError(context.Background()); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("load error = %v, want %v", err, ErrNotPaired)
	}
}
