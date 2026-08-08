package runfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxEventBytes int64  = 256 << 10
	defaultMaxLogBytes   int64  = 64 << 20
	defaultMaxEvents     uint64 = 100_000
)

// Event is the durable, workflow-neutral envelope stored in a JSONL log.
// Data is owned and validated by the consuming package.
type Event struct {
	Seq  uint64          `json:"seq"`
	At   time.Time       `json:"at"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ReadResult reports durable events and any unterminated tail. A tail is not a
// durable event until its newline has been synced; Recover or the next Append
// removes it. Malformed newline-terminated records are always corruption.
type ReadResult struct {
	Events        []Event
	ValidBytes    int64
	TornTailBytes int64
}

type EventLogOptions struct {
	MaxEventBytes int64
	MaxLogBytes   int64
	MaxEvents     uint64
	Now           func() time.Time
}

// EventLog is an append-only JSONL store with gapless sequence allocation.
// Appends are serialized in-process and with an OS file lock, then fsynced
// before returning so callers can safely record intent before performing an
// external effect.
type EventLog struct {
	scope         *Scope
	name          string
	lockKey       string
	maxEventBytes int64
	maxLogBytes   int64
	maxEvents     uint64
	now           func() time.Time
}

func NewEventLog(scope *Scope, name string, options EventLogOptions) (*EventLog, error) {
	if scope == nil {
		return nil, fmt.Errorf("runfs: nil scope")
	}
	clean, err := cleanRelative(name)
	if err != nil {
		return nil, err
	}
	if clean == "." {
		return nil, fmt.Errorf("%w: event log must be a file", ErrInvalidPath)
	}
	maxEventBytes := options.MaxEventBytes
	if maxEventBytes == 0 {
		maxEventBytes = defaultMaxEventBytes
	}
	maxLogBytes := options.MaxLogBytes
	if maxLogBytes == 0 {
		maxLogBytes = defaultMaxLogBytes
	}
	maxEvents := options.MaxEvents
	if maxEvents == 0 {
		maxEvents = defaultMaxEvents
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	if maxEventBytes < 1 || maxLogBytes < maxEventBytes || maxEvents < 1 {
		return nil, fmt.Errorf("runfs: invalid event log options")
	}
	return &EventLog{
		scope:         scope,
		name:          clean,
		lockKey:       filepath.Join(scope.canonicalRoot, filepath.FromSlash(clean)),
		maxEventBytes: maxEventBytes,
		maxLogBytes:   maxLogBytes,
		maxEvents:     maxEvents,
		now:           now,
	}, nil
}

// Append marshals data, allocates the next sequence under an exclusive lock,
// repairs only an unterminated tail, appends one newline-terminated record, and
// fsyncs it. Consumers must call Append before any effect the event protects.
func (l *EventLog) Append(ctx context.Context, eventType string, data any) (Event, error) {
	if !validEventType(eventType) {
		return Event{}, fmt.Errorf("runfs: invalid event type")
	}
	var payload json.RawMessage
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return Event{}, fmt.Errorf("encode event data: %w", err)
		}
		payload = encoded
	}

	var appended Event
	err := l.withLockedFile(ctx, func(file *os.File, created bool) error {
		parsed, err := l.readFromFile(ctx, file)
		if err != nil {
			return err
		}
		if parsed.TornTailBytes > 0 {
			if err := file.Truncate(parsed.ValidBytes); err != nil {
				return fmt.Errorf("repair torn event tail: %w", err)
			}
			if err := file.Sync(); err != nil {
				return fmt.Errorf("sync repaired event log: %w", err)
			}
		}
		if uint64(len(parsed.Events)) >= l.maxEvents {
			return ErrLogFull
		}
		now := l.now().UTC()
		if now.IsZero() {
			return fmt.Errorf("runfs: clock returned zero time")
		}
		appended = Event{
			Seq:  uint64(len(parsed.Events)) + 1,
			At:   now,
			Type: eventType,
			Data: payload,
		}
		line, err := json.Marshal(appended)
		if err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
		if int64(len(line)+1) > l.maxEventBytes {
			return ErrTooLarge
		}
		if parsed.ValidBytes+int64(len(line)+1) > l.maxLogBytes {
			return ErrLogFull
		}
		if _, err := file.Seek(parsed.ValidBytes, io.SeekStart); err != nil {
			return err
		}
		line = append(line, '\n')
		if err := writeAll(ctx, file, line); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync event log: %w", err)
		}
		if created {
			return l.scope.syncDirectory(parentPath(l.name))
		}
		return nil
	})
	if err != nil {
		return Event{}, err
	}
	return appended, nil
}

// AppendIntent is an explicit spelling for intent-before-effect call sites.
func (l *EventLog) AppendIntent(ctx context.Context, eventType string, data any) (Event, error) {
	return l.Append(ctx, eventType, data)
}

// Read validates and returns all durable events without modifying a torn tail.
func (l *EventLog) Read(ctx context.Context) (ReadResult, error) {
	var result ReadResult
	err := l.withLockedFile(ctx, func(file *os.File, _ bool) error {
		parsed, err := l.readFromFile(ctx, file)
		if err != nil {
			return err
		}
		result = parsed
		return nil
	})
	return result, err
}

// Recover validates the complete log and truncates only an unterminated tail.
// Corrupt durable lines are returned as errors and never hidden or skipped.
func (l *EventLog) Recover(ctx context.Context) (ReadResult, error) {
	var result ReadResult
	err := l.withLockedFile(ctx, func(file *os.File, _ bool) error {
		parsed, err := l.readFromFile(ctx, file)
		if err != nil {
			return err
		}
		result = parsed
		if parsed.TornTailBytes == 0 {
			return nil
		}
		if err := file.Truncate(parsed.ValidBytes); err != nil {
			return fmt.Errorf("repair torn event tail: %w", err)
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync repaired event log: %w", err)
		}
		return nil
	})
	return result, err
}

func (l *EventLog) readFromFile(ctx context.Context, file *os.File) (ReadResult, error) {
	if err := context.Cause(ctx); err != nil {
		return ReadResult{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return ReadResult{}, err
	}
	if !info.Mode().IsRegular() {
		return ReadResult{}, fmt.Errorf("%w: event log", ErrNotRegular)
	}
	if info.Size() > l.maxLogBytes {
		return ReadResult{}, ErrTooLarge
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ReadResult{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: file}, l.maxLogBytes+1))
	if err != nil {
		return ReadResult{}, err
	}
	if int64(len(raw)) > l.maxLogBytes {
		return ReadResult{}, ErrTooLarge
	}
	return l.parse(ctx, raw)
}

func (l *EventLog) parse(ctx context.Context, raw []byte) (ReadResult, error) {
	result := ReadResult{Events: make([]Event, 0)}
	for offset, lineNumber := 0, uint64(1); offset < len(raw); lineNumber++ {
		if err := context.Cause(ctx); err != nil {
			return ReadResult{}, err
		}
		relativeNewline := bytes.IndexByte(raw[offset:], '\n')
		if relativeNewline < 0 {
			result.ValidBytes = int64(offset)
			result.TornTailBytes = int64(len(raw) - offset)
			return result, nil
		}
		newline := offset + relativeNewline
		line := raw[offset:newline]
		if int64(len(line)+1) > l.maxEventBytes {
			return ReadResult{}, corrupt(lineNumber, "event exceeds size limit")
		}
		if len(bytes.TrimSpace(line)) == 0 {
			return ReadResult{}, corrupt(lineNumber, "blank durable line")
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return ReadResult{}, corrupt(lineNumber, "invalid JSON")
		}
		expected := uint64(len(result.Events)) + 1
		if event.Seq != expected {
			return ReadResult{}, corrupt(lineNumber, fmt.Sprintf("expected sequence %d, found %d", expected, event.Seq))
		}
		if event.At.IsZero() || !validEventType(event.Type) {
			return ReadResult{}, corrupt(lineNumber, "invalid event envelope")
		}
		result.Events = append(result.Events, event)
		offset = newline + 1
		result.ValidBytes = int64(offset)
	}
	return result, nil
}

func validEventType(eventType string) bool {
	return eventType != "" && eventType == strings.TrimSpace(eventType) && len(eventType) <= 128 && strings.IndexFunc(eventType, func(r rune) bool { return r < 0x20 }) < 0
}

func corrupt(line uint64, reason string) error {
	return &CorruptionError{Line: line, Reason: reason}
}

func (l *EventLog) withLockedFile(ctx context.Context, operation func(*os.File, bool) error) error {
	release, err := acquireProcessLock(ctx, l.lockKey)
	if err != nil {
		return err
	}
	defer release()

	parent := parentPath(l.name)
	if err := l.scope.ensureDirectory(ctx, parent); err != nil {
		return err
	}
	if err := l.scope.checkFinalFile(l.name, true); err != nil {
		return err
	}
	_, statErr := l.scope.root.Lstat(l.name)
	created := errors.Is(statErr, fs.ErrNotExist)
	if statErr != nil && !created {
		return statErr
	}
	file, err := l.scope.root.OpenFile(l.name, os.O_RDWR|os.O_CREATE, l.scope.fileMode)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := l.scope.checkFinalFile(l.name, false); err != nil {
		return err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := l.scope.root.Lstat(l.name)
	if err != nil {
		return err
	}
	if !os.SameFile(openedInfo, pathInfo) {
		return fmt.Errorf("%w: event log changed while opening", ErrSymlink)
	}
	if err := file.Chmod(l.scope.fileMode); err != nil {
		return err
	}
	if err := lockFile(ctx, file); err != nil {
		return err
	}
	defer unlockFile(file)
	return operation(file, created)
}

func parentPath(name string) string {
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(name)))
	if parent == "" {
		return "."
	}
	return parent
}

type processLock struct {
	token      chan struct{}
	references int
}

var (
	processLocksMu sync.Mutex
	processLocks   = make(map[string]*processLock)
)

func retainProcessLock(key string) *processLock {
	processLocksMu.Lock()
	defer processLocksMu.Unlock()
	lock := processLocks[key]
	if lock == nil {
		lock = &processLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		processLocks[key] = lock
	}
	lock.references++
	return lock
}

func releaseProcessLockReference(key string, lock *processLock) {
	processLocksMu.Lock()
	defer processLocksMu.Unlock()
	lock.references--
	if lock.references == 0 && processLocks[key] == lock {
		delete(processLocks, key)
	}
}

func processLockCount() int {
	processLocksMu.Lock()
	defer processLocksMu.Unlock()
	return len(processLocks)
}

func acquireProcessLock(ctx context.Context, key string) (func(), error) {
	lock := retainProcessLock(key)
	select {
	case <-ctx.Done():
		releaseProcessLockReference(key, lock)
		return nil, context.Cause(ctx)
	case <-lock.token:
		return func() {
			lock.token <- struct{}{}
			releaseProcessLockReference(key, lock)
		}, nil
	}
}
