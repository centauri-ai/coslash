package atlas

import "sync"

var atlasWriteLocks keyLockSet

// keyLockSet serializes compound read-check-write operations without retaining
// one mutex forever for every board or run ever opened.
type keyLockSet struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

func (s *keyLockSet) lock(key string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = make(map[string]*keyLock)
	}
	entry := s.locks[key]
	if entry == nil {
		entry = &keyLock{}
		s.locks[key] = entry
	}
	entry.refs++
	s.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
	}
}

func (s *keyLockSet) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.locks)
}
