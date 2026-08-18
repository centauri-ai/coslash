// Package sessionpreview exposes canonical session-snapshot/v1 bytes for preview and upload.
package sessionpreview

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

const AdapterVersion = "snapshot-preview/v1"

type State string

const (
	StateReady              State = "ready"
	StateInvalid            State = "invalid"
	StateUnsupportedVersion State = "unsupported_version"
	StateStaleSource        State = "stale_source"
	StateOversized          State = "oversized"
)

type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Action  string `json:"action"`
}

type Response struct {
	AdapterVersion         string               `json:"adapterVersion"`
	State                  State                `json:"state"`
	ApprovalAllowed        bool                 `json:"approvalAllowed"`
	SourceRevision         int64                `json:"sourceRevision"`
	SchemaVersion          string               `json:"schemaVersion,omitempty"`
	MediaType              string               `json:"mediaType,omitempty"`
	PayloadBytes           int                  `json:"payloadBytes,omitempty"`
	MaxPayloadBytes        int                  `json:"maxPayloadBytes"`
	CanonicalPayloadBase64 string               `json:"canonicalPayloadBase64,omitempty"`
	Snapshot               *snapshotv1.Snapshot `json:"snapshot,omitempty"`
	Problem                *Problem             `json:"problem,omitempty"`
}

func Build(local session.Session, options sessionexport.BuildOptions, expectedRevision int64) Response {
	if expectedRevision != local.LastActivityTime {
		return blocked(StateStaleSource, local.LastActivityTime, Problem{
			Code:    "source_changed",
			Message: "This session changed after it was selected, so the previous preview is stale.",
			Action:  "Refresh the session list and review the current snapshot before sharing.",
		})
	}
	payload, err := sessionexport.Marshal(local, options)
	if err != nil {
		return fromError(err, local.LastActivityTime)
	}
	return FromCanonical(payload, local.LastActivityTime)
}

func FromCanonical(payload []byte, sourceRevision int64) Response {
	if len(payload) > snapshotv1.MaxPayloadBytes {
		return blocked(StateOversized, sourceRevision, Problem{
			Code:    "aggregate_size_exceeded",
			Message: fmt.Sprintf("The canonical snapshot is %d bytes; the limit is %d bytes.", len(payload), snapshotv1.MaxPayloadBytes),
			Action:  "Reduce the session evidence before previewing it again.",
		})
	}
	var envelope struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil &&
		strings.TrimSpace(envelope.SchemaVersion) != "" &&
		envelope.SchemaVersion != snapshotv1.SchemaVersion {
		return blocked(StateUnsupportedVersion, sourceRevision, Problem{
			Code:    "unsupported_schema_version",
			Message: fmt.Sprintf("Snapshot version %q is not supported by this preview.", envelope.SchemaVersion),
			Action:  "Update coSlash before reviewing or sharing this session.",
		})
	}
	snapshot, err := snapshotv1.Decode(payload)
	if err != nil {
		return fromError(err, sourceRevision)
	}
	return Response{
		AdapterVersion:         AdapterVersion,
		State:                  StateReady,
		ApprovalAllowed:        true,
		SourceRevision:         sourceRevision,
		SchemaVersion:          snapshot.SchemaVersion,
		MediaType:              snapshot.MediaType,
		PayloadBytes:           len(payload),
		MaxPayloadBytes:        snapshotv1.MaxPayloadBytes,
		CanonicalPayloadBase64: base64.StdEncoding.EncodeToString(payload),
		Snapshot:               &snapshot,
	}
}

func UploadBytes(response Response) ([]byte, error) {
	if response.State != StateReady || !response.ApprovalAllowed || response.Snapshot == nil {
		return nil, fmt.Errorf("preview state %q is not approvable", response.State)
	}
	payload, err := base64.StdEncoding.DecodeString(response.CanonicalPayloadBase64)
	if err != nil {
		return nil, fmt.Errorf("decode canonical preview payload: %w", err)
	}
	decoded, err := snapshotv1.Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("validate canonical preview payload: %w", err)
	}
	if !reflect.DeepEqual(decoded, *response.Snapshot) {
		return nil, fmt.Errorf("preview snapshot does not match canonical upload payload")
	}
	if len(payload) != response.PayloadBytes {
		return nil, fmt.Errorf("preview payload length is %d; metadata says %d", len(payload), response.PayloadBytes)
	}
	return payload, nil
}

func fromError(err error, sourceRevision int64) Response {
	if errors.Is(err, snapshotv1.ErrOversized) {
		return blocked(StateOversized, sourceRevision, Problem{
			Code:    "aggregate_size_exceeded",
			Message: "The canonical snapshot cannot fit within the aggregate payload limit.",
			Action:  "Reduce the session evidence before previewing it again.",
		})
	}
	return blocked(StateInvalid, sourceRevision, Problem{
		Code:    "invalid_snapshot",
		Message: "This session cannot produce a valid canonical snapshot.",
		Action:  "Update coSlash or repair the source session, then preview it again.",
	})
}

func blocked(state State, sourceRevision int64, problem Problem) Response {
	return Response{
		AdapterVersion:  AdapterVersion,
		State:           state,
		ApprovalAllowed: false,
		SourceRevision:  sourceRevision,
		MaxPayloadBytes: snapshotv1.MaxPayloadBytes,
		Problem:         &problem,
	}
}
