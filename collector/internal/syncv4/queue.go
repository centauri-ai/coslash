package syncv4

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const recentWindow = 72 * time.Hour

type Entry struct {
	Key                  string                          `json:"key"`
	Selection            sessionbackupproducer.Selection `json:"selection"`
	Session              hubclient.V4Session             `json:"session"`
	Activity             int64                           `json:"activity"`
	SyncedActivity       int64                           `json:"syncedActivity"`
	SourceRevision       string                          `json:"sourceRevision,omitempty"`
	SyncedSourceRevision string                          `json:"syncedSourceRevision,omitempty"`
	Recent               bool                            `json:"recent"`
	BundleID             string                          `json:"bundleId,omitempty"`
	ContentSHA256        string                          `json:"contentSha256,omitempty"`
	Manifest             *hubclient.V4Manifest           `json:"manifest,omitempty"`
	UploadID             string                          `json:"uploadId,omitempty"`
	SessionID            string                          `json:"sessionId,omitempty"`
	RevisionID           string                          `json:"revisionId,omitempty"`
	FailureCode          string                          `json:"failureCode,omitempty"`
	Attempt              int                             `json:"attempt,omitempty"`
	Excluded             bool                            `json:"excluded,omitempty"`
}

type state struct {
	Version   int     `json:"version"`
	InstallID string  `json:"installId"`
	Binding   string  `json:"binding,omitempty"`
	Entries   []Entry `json:"entries"`
}

type Queue struct {
	mu    sync.Mutex
	path  string
	state state
}

func Open(root string) (*Queue, error) {
	if root == "" {
		root = filepath.Join(settings.Home(), "sync-v4")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := protectQueueDirectory(root); err != nil {
		return nil, err
	}
	q := &Queue{path: filepath.Join(root, "queue.json")}
	data, err := readQueueFile(q.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil && len(data) == 0 {
		return nil, errors.New("empty v4 queue")
	}
	if len(data) != 0 {
		if err := json.Unmarshal(data, &q.state); err != nil || q.state.Version != 1 || q.state.InstallID == "" {
			return nil, errors.New("invalid v4 queue")
		}
		return q, nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	q.state = state{Version: 1, InstallID: fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])}
	if err := q.save(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) InstallID() string { q.mu.Lock(); defer q.mu.Unlock(); return q.state.InstallID }

func (q *Queue) Rebind(binding string) error {
	if len(binding) != 64 {
		return errors.New("invalid v4 binding")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.Binding == binding {
		return nil
	}
	if q.state.Binding != "" {
		for i := range q.state.Entries {
			entry := &q.state.Entries[i]
			entry.SyncedActivity, entry.SyncedSourceRevision = 0, ""
			entry.BundleID, entry.ContentSHA256, entry.UploadID, entry.SessionID, entry.RevisionID, entry.FailureCode = "", "", "", "", "", ""
			entry.Manifest, entry.Attempt = nil, 0
		}
	}
	q.state.Binding = binding
	return q.save()
}

func (q *Queue) Entries() []Entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	entries := append([]Entry(nil), q.state.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Recent != entries[j].Recent {
			return entries[i].Recent
		}
		if entries[i].Activity != entries[j].Activity {
			return entries[i].Activity > entries[j].Activity
		}
		return entries[i].Key < entries[j].Key
	})
	return entries
}

func (q *Queue) Merge(found []Entry, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	index := make(map[string]int, len(q.state.Entries))
	for i, entry := range q.state.Entries {
		index[entry.Key] = i
	}
	for _, item := range found {
		if item.Key == "" || item.Session.Agent == "" {
			continue
		}
		if i, ok := index[item.Key]; ok {
			entry := &q.state.Entries[i]
			entry.Recent = entry.Recent || item.Activity >= now.Add(-recentWindow).UnixMilli()
			if item.Activity > entry.Activity || item.SourceRevision != "" && item.SourceRevision != entry.SourceRevision {
				entry.Activity = max(entry.Activity, item.Activity)
				entry.SourceRevision = item.SourceRevision
				entry.Session = item.Session
				entry.BundleID, entry.ContentSHA256, entry.UploadID, entry.FailureCode = "", "", "", ""
				entry.Manifest, entry.RevisionID = nil, ""
				entry.Attempt = 0
			}
		} else {
			item.Recent = item.Activity >= now.Add(-recentWindow).UnixMilli()
			index[item.Key] = len(q.state.Entries)
			q.state.Entries = append(q.state.Entries, item)
		}
	}
	return q.save()
}

func (q *Queue) Update(entry Entry) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.state.Entries {
		if q.state.Entries[i].Key == entry.Key {
			if entry.Activity < q.state.Entries[i].Activity || entry.SourceRevision != q.state.Entries[i].SourceRevision {
				return errors.New("stale v4 queue entry")
			}
			q.state.Entries[i] = entry
			return q.save()
		}
	}
	return errors.New("v4 queue entry missing")
}

func (q *Queue) Progress() hubclient.V4Queue {
	q.mu.Lock()
	defer q.mu.Unlock()
	var progress hubclient.V4Queue
	progress.FirstSync.HistoryState = "complete"
	for _, entry := range q.state.Entries {
		if entry.Excluded {
			continue
		}
		if entry.Recent {
			progress.FirstSync.RecentTotal++
			if !pending(entry) {
				progress.FirstSync.RecentDone++
			}
		} else if pending(entry) {
			progress.FirstSync.HistoryState = "syncing"
		}
		if pending(entry) {
			progress.Pending++
		}
		if entry.FailureCode != "" {
			progress.Failing++
		}
	}
	return progress
}

func pending(entry Entry) bool {
	return entry.RevisionID == "" || entry.SyncedActivity < entry.Activity || entry.SourceRevision != "" && entry.SyncedSourceRevision != entry.SourceRevision
}

func (q *Queue) save() error {
	data, err := json.Marshal(q.state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(q.path), ".queue-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := protectQueueFile(file.Name(), file); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), q.path)
}
