package contracts

import (
	"encoding/json"
	"time"
)

// BoardDocument freezes the common revision envelope while leaving the board
// schema to DaGama or Atlas. Its fields preserve the legacy board CRUD shape.
type BoardDocument struct {
	SchemaVersion uint64          `json:"schemaVersion"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Revision      uint64          `json:"revision"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	Board         json.RawMessage `json:"board"`
}

// RunDocument freezes the fields shared by the legacy DaGama and Atlas run
// documents. Product packages embed this header and define their own validated
// status and state fields. Sessions is an additive composite-identity binding.
type RunDocument struct {
	SchemaVersion uint64            `json:"schemaVersion"`
	RunID         string            `json:"runId"`
	ProjectID     string            `json:"projectId"`
	BoardID       string            `json:"boardId"`
	BoardRevision uint64            `json:"boardRevision"`
	Title         string            `json:"title"`
	Status        string            `json:"status"`
	Sessions      []SessionIdentity `json:"sessions,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
	FinishedAt    *time.Time        `json:"finishedAt"`
}
