package persistence

import (
	"context"
	"sync"
)

// keyLocks serializes read-modify-write cycles per document within one process.
//
// Entries are reference counted and removed once the last holder releases them,
// so a long-lived collector that touches many sessions does not accumulate one
// mutex per session forever.
type keyLocks struct {
	mu      sync.Mutex
	entries map[string]*keyLockEntry
}

type keyLockEntry struct {
	references int
	channel    chan struct{}
}

func newKeyLocks() *keyLocks {
	return &keyLocks{entries: make(map[string]*keyLockEntry)}
}

// acquire blocks until the key is held or the context ends. The returned
// release function is safe to call exactly once.
func (l *keyLocks) acquire(ctx context.Context, key string) (func(), error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}

	l.mu.Lock()
	entry, found := l.entries[key]
	if !found {
		entry = &keyLockEntry{channel: make(chan struct{}, 1)}
		l.entries[key] = entry
	}
	entry.references++
	l.mu.Unlock()

	select {
	case entry.channel <- struct{}{}:
		return func() { l.release(key, entry, true) }, nil
	case <-ctx.Done():
		l.release(key, entry, false)
		return nil, context.Cause(ctx)
	}
}

func (l *keyLocks) release(key string, entry *keyLockEntry, held bool) {
	if held {
		<-entry.channel
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.references--
	if entry.references <= 0 {
		delete(l.entries, key)
	}
}

// size reports the number of retained entries. It exists for resource-leak
// assertions in tests.
func (l *keyLocks) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
