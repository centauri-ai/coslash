// Package sessionbackupproducer freezes complete local and SSH Codex session
// families into retryable session-backup/v1 spools.
package sessionbackupproducer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

var (
	ErrNotPrepared = errors.New("session backup is not prepared")
	ErrIncomplete  = errors.New("session backup preparation is incomplete")
)

type Selection struct {
	SourceKind string `json:"sourceKind"`
	SourceID   string `json:"sourceId"`
	Agent      string `json:"agent"`
	SessionID  string `json:"sessionId"`
}

type Coverage struct {
	ArtifactCount  int                              `json:"artifactCount"`
	ArtifactCounts []sessionbackupv1.ArtifactCount  `json:"artifactCounts"`
	TotalBytes     int64                            `json:"totalBytes"`
	RevisionSHA256 string                           `json:"revisionSha256"`
	Problems       []sessionbackupv1.CaptureProblem `json:"problems"`
}

type Prepared struct {
	BundleID  string                   `json:"bundleId"`
	Selection Selection                `json:"selection"`
	Manifest  sessionbackupv1.Manifest `json:"manifest"`
	Coverage  Coverage                 `json:"coverage"`
}

const (
	StatePreparing = "preparing"
	StateReady     = "ready"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

type Preparation struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Selection Selection `json:"selection"`
	Prepared  *Prepared `json:"prepared,omitempty"`
	Coverage  Coverage  `json:"coverage"`
}

type operation struct {
	state  Preparation
	cancel context.CancelFunc
	done   chan struct{}
}

type PreparationError struct {
	Coverage Coverage
}

func (err *PreparationError) Error() string {
	if len(err.Coverage.Problems) == 0 {
		return ErrIncomplete.Error()
	}
	return fmt.Sprintf("%s: %s", ErrIncomplete, err.Coverage.Problems[0].Code)
}

func (err *PreparationError) Unwrap() error { return ErrIncomplete }

type SynthesisStore interface {
	LoadRecord(agent, id string) (synthesis.Record, error)
}

type SourceHandle struct {
	Source vendors.ReadSource
	Home   string
	Close  func() error
}

type OpenSource func(context.Context, Selection) (SourceHandle, error)

type Options struct {
	Root             string
	CollectorVersion string
	ParserVersion    string
	Remote           *remote.Manager
	Synthesis        SynthesisStore
	OpenSource       OpenSource
	LocalHome        func() (string, error)
	AfterRawCopy     func() // deterministic mutation hook for focused tests
}

type Manager struct {
	root             string
	collectorVersion string
	parserVersion    string
	synthesis        SynthesisStore
	openSource       OpenSource
	afterRawCopy     func()
	mu               sync.Mutex
	operations       map[string]*operation
}

func New(options Options) *Manager {
	root := options.Root
	if root == "" {
		root = filepath.Join(settings.Home(), "session-backups", "prepared")
	}
	localHome := options.LocalHome
	if localHome == nil {
		localHome = os.UserHomeDir
	}
	openSource := options.OpenSource
	if openSource == nil {
		openSource = func(ctx context.Context, selection Selection) (SourceHandle, error) {
			switch selection.SourceKind {
			case sessionbackupv1.SourceLocal:
				home, err := localHome()
				return SourceHandle{Source: vendors.LocalReadSource, Home: home}, err
			case sessionbackupv1.SourceSSH:
				if options.Remote == nil {
					return SourceHandle{}, remote.ErrRemoteSessionUnavailable
				}
				remoteSession, err := options.Remote.OpenBackupSession(ctx, selection.SourceID)
				if err != nil {
					return SourceHandle{}, err
				}
				return SourceHandle{
					Source: remoteSession.Source().ForVendor(1 << 30), Home: remoteSession.Source().Home(),
					Close: remoteSession.Close,
				}, nil
			default:
				return SourceHandle{}, ErrIncomplete
			}
		}
	}
	return &Manager{
		root: root, collectorVersion: identifierOr(options.CollectorVersion, "development"),
		parserVersion: identifierOr(options.ParserVersion, vendors.ParserVersion),
		synthesis:     options.Synthesis, openSource: openSource, afterRawCopy: options.AfterRawCopy,
		operations: map[string]*operation{},
	}
}

// Start begins a cancellable preparation without occupying the caller's
// request goroutine. Status and Wait expose the product-visible preparing
// state while the source is being frozen.
func (manager *Manager) Start(ctx context.Context, selection Selection) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	id := hex.EncodeToString(random)
	operationContext, cancel := context.WithCancel(ctx)
	item := &operation{
		state:  Preparation{ID: id, State: StatePreparing, Selection: selection, Coverage: Coverage{Problems: []sessionbackupv1.CaptureProblem{}}},
		cancel: cancel, done: make(chan struct{}),
	}
	manager.mu.Lock()
	manager.operations[id] = item
	manager.mu.Unlock()
	go func() {
		prepared, err := manager.Prepare(operationContext, selection)
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if operationContext.Err() != nil {
			item.state.State = StateCancelled
			item.state.Coverage = Coverage{Problems: []sessionbackupv1.CaptureProblem{{
				Code: sessionbackupv1.ProblemUnavailable, MemberID: selection.SessionID, Retryable: true,
			}}}
		} else if err != nil {
			item.state.State = StateFailed
			var preparationError *PreparationError
			if errors.As(err, &preparationError) {
				item.state.Coverage = preparationError.Coverage
			}
		} else {
			item.state.State = StateReady
			item.state.Prepared = prepared
			item.state.Coverage = prepared.Coverage
		}
		close(item.done)
	}()
	return id, nil
}

func (manager *Manager) Status(id string) (Preparation, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	item, ok := manager.operations[id]
	if !ok {
		return Preparation{}, false
	}
	return item.state, true
}

func (manager *Manager) Cancel(id string) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	item, ok := manager.operations[id]
	if !ok || item.state.State != StatePreparing {
		return false
	}
	item.cancel()
	return true
}

func (manager *Manager) Wait(ctx context.Context, id string) (Preparation, error) {
	manager.mu.Lock()
	item, ok := manager.operations[id]
	manager.mu.Unlock()
	if !ok {
		return Preparation{}, ErrNotPrepared
	}
	select {
	case <-ctx.Done():
		return Preparation{}, ctx.Err()
	case <-item.done:
		state, _ := manager.Status(id)
		return state, nil
	}
}

func identifierOr(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func coverage(manifest sessionbackupv1.Manifest) Coverage {
	return Coverage{
		ArtifactCount: manifest.Summary.ArtifactCount, ArtifactCounts: manifest.Summary.ArtifactCounts,
		TotalBytes: manifest.Summary.TotalBytes, RevisionSHA256: manifest.CompleteBackupSHA256,
		Problems: []sessionbackupv1.CaptureProblem{},
	}
}

func problem(selection Selection, code, kind string, retryable bool) (*Prepared, error) {
	report := Coverage{Problems: []sessionbackupv1.CaptureProblem{{
		Code: code, MemberID: selection.SessionID, Kind: kind, Retryable: retryable,
	}}}
	return nil, &PreparationError{Coverage: report}
}

func (manager *Manager) Open(bundleID string) (*Prepared, error) {
	if !digest(bundleID) {
		return nil, ErrNotPrepared
	}
	root := filepath.Join(manager.root, bundleID)
	manifestBytes, err := os.ReadFile(filepath.Join(root, sessionbackupv1.ManifestFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotPrepared
		}
		return nil, err
	}
	manifest, err := sessionbackupv1.Decode(manifestBytes)
	if err != nil || manifest.CompleteBackupSHA256 != bundleID {
		return nil, ErrNotPrepared
	}
	if err := verifySpool(root, manifest); err != nil {
		return nil, ErrNotPrepared
	}
	return &Prepared{
		BundleID:  bundleID,
		Selection: Selection{SourceKind: manifest.Source.Kind, SourceID: manifest.Source.SourceID, Agent: manifest.Source.Agent, SessionID: manifest.Family.RootMemberID},
		Manifest:  manifest, Coverage: coverage(manifest),
	}, nil
}

// Read reads a bounded caller-owned slice from one declared artifact. It never
// materializes the complete artifact in memory.
func (manager *Manager) Read(bundleID, logicalName string, offset int64, destination []byte) (int, error) {
	if !digest(bundleID) {
		return 0, ErrNotPrepared
	}
	manifestBytes, err := os.ReadFile(filepath.Join(manager.root, bundleID, sessionbackupv1.ManifestFileName))
	if err != nil {
		return 0, ErrNotPrepared
	}
	manifest, err := sessionbackupv1.Decode(manifestBytes)
	if err != nil || manifest.CompleteBackupSHA256 != bundleID {
		return 0, ErrNotPrepared
	}
	declared := false
	for _, artifact := range manifest.Artifacts {
		if artifact.LogicalName == logicalName {
			declared = true
			break
		}
	}
	if !declared || offset < 0 {
		return 0, ErrNotPrepared
	}
	file, err := openRegular(manager.root, bundleID, logicalName)
	if err != nil {
		return 0, ErrNotPrepared
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	return file.Read(destination)
}

func (manager *Manager) Discard(bundleID string) error {
	if !digest(bundleID) {
		return ErrNotPrepared
	}
	path := filepath.Join(manager.root, bundleID)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return ErrNotPrepared
	} else if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func verifySpool(root string, manifest sessionbackupv1.Manifest) error {
	declared := map[string]bool{sessionbackupv1.ManifestFileName: true}
	buffer := make([]byte, 128*1024)
	for _, artifact := range manifest.Artifacts {
		file, err := openRegularRoot(root, artifact.LogicalName)
		if err != nil {
			return err
		}
		hash := sha256.New()
		count, copyErr := io.CopyBuffer(hash, file, buffer)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || count != artifact.ByteLength || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
			return ErrNotPrepared
		}
		declared[filepath.FromSlash(artifact.LogicalName)] = true
	}
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(root, name)
		if err != nil || !declared[relative] {
			return ErrNotPrepared
		}
		return nil
	})
}

func openRegular(root, bundleID, logicalName string) (*os.File, error) {
	if !digest(bundleID) {
		return nil, ErrNotPrepared
	}
	return openRegularRoot(filepath.Join(root, bundleID), logicalName)
}

func openRegularRoot(root, logicalName string) (*os.File, error) {
	if logicalName == "" || filepath.IsAbs(logicalName) || strings.Contains(logicalName, "\\") {
		return nil, ErrNotPrepared
	}
	current := root
	var final fs.FileInfo
	for _, part := range strings.Split(filepath.FromSlash(logicalName), string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return nil, ErrNotPrepared
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrNotPrepared
		}
		final = info
	}
	if final == nil || !final.Mode().IsRegular() {
		return nil, ErrNotPrepared
	}
	return os.Open(current)
}

func digest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
