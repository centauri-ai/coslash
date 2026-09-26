package directedhandoff

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const markerPrefix = "coSlash handoff ID: "

type Record struct {
	ID              string `json:"id"`
	SourceID        string `json:"sourceId"`
	SourceAgent     string `json:"sourceAgent"`
	SourceSessionID string `json:"sourceSessionId"`
	TargetAgent     string `json:"targetAgent"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	CreatedAt       int64  `json:"createdAt"`
	TargetSessionID string `json:"targetSessionId,omitempty"`
	Result          string `json:"result,omitempty"`
	Error           string `json:"error,omitempty"`
	Activity        string `json:"activity,omitempty"`
}

type Store struct {
	mu      sync.Mutex
	path    string
	records []Record
	now     func() time.Time
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
	stopped bool
}

func Open(path string) (*Store, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Store{path: path, now: time.Now, records: []Record{}, ctx: ctx, cancel: cancel}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.records); err != nil {
		return nil, err
	}
	return s, nil
}

func Marker(id string) string { return markerPrefix + id }

func (s *Store) Start(sourceID, sourceAgent, sourceSessionID, targetAgent, kind string) (Record, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Record{}, err
	}
	record := Record{ID: hex.EncodeToString(random[:]), SourceID: sourceID, SourceAgent: sourceAgent,
		SourceSessionID: sourceSessionID, TargetAgent: targetAgent, Kind: kind, Status: "running",
		CreatedAt: s.now().UnixMilli(), Activity: "starting"}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append([]Record{record}, s.records...)
	if err := s.save(); err != nil {
		s.records = s.records[1:]
		return Record{}, err
	}
	return record, nil
}

func (s *Store) List() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record{}, s.records...)
}

func (s *Store) RunReview(run func(context.Context)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		run(s.ctx)
	}()
	return true
}

func (s *Store) Shutdown() {
	s.mu.Lock()
	s.stopped = true
	s.cancel()
	s.mu.Unlock()
	s.workers.Wait()
}

func (s *Store) RecoverReviews() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := append([]Record(nil), s.records...)
	changed := false
	for i := range s.records {
		r := &s.records[i]
		if r.Kind != "review" || r.Status != "running" {
			continue
		}
		r.Status = "failed"
		r.Activity = ""
		r.Error = "Review was interrupted when coSlash stopped. Start a new review."
		changed = true
	}
	if !changed {
		return nil
	}
	if err := s.save(); err != nil {
		s.records = previous
		return err
	}
	return nil
}

func (s *Store) Complete(id, result string) error { return s.finish(id, "completed", result) }
func (s *Store) Fail(id, message string) error    { return s.finish(id, "failed", message) }

func (s *Store) finish(id, status, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.records {
		if s.records[i].ID != id || s.records[i].Status != "running" {
			continue
		}
		before := s.records[i]
		s.records[i].Status = status
		s.records[i].Activity = ""
		if status == "completed" {
			s.records[i].Result = value
		} else {
			s.records[i].Error = value
		}
		if err := s.save(); err != nil {
			s.records[i] = before
			return err
		}
		return nil
	}
	return nil
}

func (s *Store) Observe(sourceID string, sessions []*session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	previous := append([]Record(nil), s.records...)
	for i := range s.records {
		r := &s.records[i]
		if r.SourceID != sourceID || r.Status != "running" || r.Kind != "custom" {
			continue
		}
		var target *session.Session
		for _, candidate := range sessions {
			if candidate == nil || candidate.Agent != r.TargetAgent {
				continue
			}
			if r.TargetSessionID != "" {
				if candidate.ID == r.TargetSessionID {
					target = candidate
					break
				}
			} else if candidate.FirstPrompt != nil && containsMarker(*candidate.FirstPrompt, r.ID) {
				target = candidate
				break
			}
		}
		if target == nil {
			if r.TargetSessionID == "" && s.now().Sub(time.UnixMilli(r.CreatedAt)) >= time.Minute && r.Activity != "not_detected" {
				r.Activity = "not_detected"
				changed = true
			}
			continue
		}
		if r.TargetSessionID != target.ID {
			r.TargetSessionID = target.ID
			changed = true
		}
		activity := "working"
		if target.Status != nil && (*target.Status == "idle" || *target.Status == "waiting") {
			activity = "needs_input"
		}
		if r.Activity != activity {
			r.Activity = activity
			changed = true
		}
		if r.Kind == "custom" {
			for _, entry := range target.Digest {
				if entry.Turn == 1 && entry.Category == session.DigestRecap && strings.TrimSpace(entry.Description) != "" {
					r.Status, r.Result, r.Activity = "completed", entry.Description, ""
					changed = true
					break
				}
			}
			if r.Status == "running" && target.Status != nil && *target.Status == "inactive" {
				r.Status, r.Error, r.Activity = "failed", "Target stopped before returning a recap.", ""
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	if err := s.save(); err != nil {
		s.records = previous
		return err
	}
	return nil
}

func containsMarker(prompt, id string) bool {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.TrimSpace(line) == Marker(id) {
			return true
		}
	}
	return false
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s.records)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".handoffs-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
