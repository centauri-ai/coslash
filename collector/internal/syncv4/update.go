package syncv4

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/google/uuid"
)

var ErrUpdateAlreadyCurrent = errors.New("update target already installed")
var errUpdateAlreadyFailed = errors.New("update already failed")

func (q *Queue) SignalUpdate() {
	select {
	case q.updateWake <- struct{}{}:
	default:
	}
}
func (q *Queue) UpdateWake() <-chan struct{}     { return q.updateWake }
func (q *Queue) CompletionWake() <-chan struct{} { return q.completionWake }

// CheckInForUpdate applies the normal check-in response without running uploads.
func (r *Runner) CheckInForUpdate(ctx context.Context) error { return r.refreshConsent(ctx) }

// ReportUpdate sends only the update receipt, without running the upload pass
// or changing its retry deadlines or backoff.
func (r *Runner) ReportUpdate(ctx context.Context) error {
	update := r.Queue.PendingUpdate()
	if update == nil {
		return nil
	}
	var results []hubclient.V4CommandResult
	for _, result := range r.Queue.Results() {
		if result.CommandID == update.ID {
			results = append(results, result)
			break
		}
	}
	if len(results) == 0 {
		return nil
	}
	if results[0].Result == "done" {
		return nil // Completion requires a normal, version-bearing check-in.
	}
	transport, ok := r.Hub.(interface {
		V4ReportUpdate(context.Context, hubclient.V4CommandResult) (bool, error)
	})
	if !ok {
		return errors.New("update reporting unavailable")
	}
	active, err := transport.V4ReportUpdate(ctx, results[0])
	if err != nil {
		return err
	}
	if active {
		return nil
	}
	return r.Queue.ReconcileUpdate(r.Version, nil, results, true)
}

// UpdateAttempt is persisted with the paired queue before any update starts.
type UpdateAttempt struct {
	ID               string    `json:"attemptId"`
	TargetVersion    string    `json:"targetVersion"`
	ExpiresAt        time.Time `json:"expiresAt"`
	Stage            string    `json:"stage"`
	Error            string    `json:"error,omitempty"`
	HandoffReady     bool      `json:"handoffReady,omitempty"`
	LegacySettlement bool      `json:"legacySettlement,omitempty"`
}

func (q *Queue) AcceptUpdate(command hubclient.V4Command, current string, now time.Time) error {
	if len(command.Payload) > 512 {
		return errors.New("invalid update payload")
	}
	var payload struct {
		AttemptID        string    `json:"attemptId"`
		TargetVersion    string    `json:"targetVersion"`
		ExpiresAt        time.Time `json:"expiresAt"`
		LegacySettlement bool      `json:"legacySettlement,omitempty"`
	}
	d := json.NewDecoder(bytes.NewReader(command.Payload))
	d.DisallowUnknownFields()
	if d.Decode(&payload) != nil || d.Decode(&struct{}{}) != io.EOF || payload.AttemptID != command.ID || uuid.Validate(command.ID) != nil {
		return errors.New("invalid update")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if previous := q.state.Update; previous != nil {
		if previous.ID == command.ID && previous.TargetVersion == payload.TargetVersion && previous.ExpiresAt.Equal(payload.ExpiresAt) && previous.LegacySettlement == payload.LegacySettlement {
			return nil
		}
		return errors.New("another update is active")
	}
	if strings.ContainsAny(payload.TargetVersion, "-+") || !hubclient.ValidClientVersion(payload.TargetVersion) || !hubclient.ValidClientVersion(current) {
		return errors.New("invalid update version")
	}
	if payload.LegacySettlement && hubclient.NewerClientVersion(current, payload.TargetVersion) {
		return errors.New("legacy settlement requires an installed target")
	}
	if !payload.LegacySettlement && !hubclient.NewerClientVersion(current, payload.TargetVersion) {
		return ErrUpdateAlreadyCurrent
	}
	if payload.ExpiresAt.IsZero() || (!payload.LegacySettlement && !payload.ExpiresAt.After(now)) || payload.ExpiresAt.After(now.Add(8*24*time.Hour)) {
		return errors.New("invalid or expired update")
	}
	stage := "picked_up"
	if payload.LegacySettlement {
		stage = "finalizing"
	}
	q.state.Update = &UpdateAttempt{ID: command.ID, TargetVersion: payload.TargetVersion, ExpiresAt: payload.ExpiresAt, Stage: stage, LegacySettlement: payload.LegacySettlement}
	if err := q.save(); err != nil {
		q.state.Update = nil
		return err
	}
	return nil
}

func (q *Queue) PendingUpdate() *UpdateAttempt {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.Update == nil {
		return nil
	}
	copy := *q.state.Update
	return &copy
}

func (q *Queue) SetUpdateStage(id, stage, code string) error {
	if stage != "picked_up" && stage != "preparing" && stage != "finalizing" && stage != "failed" {
		return errors.New("invalid update stage")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.Update == nil || q.state.Update.ID != id {
		return errors.New("update is no longer active")
	}
	if q.state.Update.Stage == "failed" && stage != "failed" {
		return errUpdateAlreadyFailed
	}
	prior := *q.state.Update
	q.state.Update.Stage, q.state.Update.Error = stage, code
	if err := q.save(); err != nil {
		*q.state.Update = prior
		return err
	}
	return nil
}

func (q *Queue) ConfirmUpdateHandoff(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state.Update == nil || q.state.Update.ID != id {
		return errors.New("update is no longer active")
	}
	if q.state.Update.Stage == "failed" {
		return errUpdateAlreadyFailed
	}
	prior := *q.state.Update
	q.state.Update.HandoffReady = true
	if q.state.Update.LegacySettlement {
		q.state.Update.Stage, q.state.Update.Error = "finalizing", ""
	}
	if err := q.save(); err != nil {
		*q.state.Update = prior
		return err
	}
	select {
	case q.completionWake <- struct{}{}:
	default:
	}
	return nil
}

// ReconcileUpdate clears only an attempt whose report the Hub just accepted
// and no longer redelivers. This keeps a lost response safe to retry.
func (q *Queue) ReconcileUpdate(current string, delivered []hubclient.V4Command, sent []hubclient.V4CommandResult, updateAware bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.state.Update
	if u == nil {
		return nil
	}
	for _, c := range delivered {
		if c.ID == u.ID {
			return nil
		}
	}
	reported := false
	completionSent := false
	failureSent := false
	for _, r := range sent {
		if r.CommandID == u.ID {
			reported = true
			completionSent = r.Result == "done"
			failureSent = r.Result == "failed"
			break
		}
	}
	if !reported || (u.Stage != "failed" && current != u.TargetVersion && !hubclient.NewerClientVersion(u.TargetVersion, current)) {
		return nil
	}
	if u.Stage == "failed" && (!failureSent || !updateAware) {
		return nil
	}
	if u.LegacySettlement && (!u.HandoffReady || !completionSent || !updateAware) {
		return nil
	}
	if u.Stage != "failed" && current == u.TargetVersion && (!u.HandoffReady || !completionSent || !updateAware) {
		return nil
	}
	q.state.Update = nil
	if err := q.save(); err != nil {
		q.state.Update = u
		return err
	}
	return nil
}
