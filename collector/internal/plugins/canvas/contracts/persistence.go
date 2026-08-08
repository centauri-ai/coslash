package contracts

import (
	"encoding/json"
	"time"
)

// WorkspaceDocument is a revisioned server-backed Canvas workspace. State is
// owned and validated by the consuming Canvas package.
type WorkspaceDocument struct {
	SchemaVersion uint64          `json:"schemaVersion"`
	Revision      uint64          `json:"revision"`
	Session       SessionIdentity `json:"session"`
	State         json.RawMessage `json:"state"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

// WorkspaceWrite is an optimistic write against an existing revision.
type WorkspaceWrite struct {
	SchemaVersion    uint64          `json:"schemaVersion"`
	ExpectedRevision uint64          `json:"expectedRevision"`
	State            json.RawMessage `json:"state"`
}
