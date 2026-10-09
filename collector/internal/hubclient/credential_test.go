package hubclient

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type observedDoneContext struct {
	context.Context
	doneCalled chan struct{}
	once       sync.Once
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.doneCalled) })
	return ctx.Context.Done()
}

func TestOSKeychainMutationLockHonorsCanceledContext(t *testing.T) {
	if err := lockOSKeychainMutation(context.Background()); err != nil {
		t.Fatalf("lock keychain: %v", err)
	}
	defer unlockOSKeychainMutation()

	base, cancel := context.WithCancel(context.Background())
	ctx := &observedDoneContext{Context: base, doneCalled: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- lockOSKeychainMutation(ctx) }()
	<-ctx.doneCalled
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("lock with canceled context = %v, want %v", err, context.Canceled)
	}
}
