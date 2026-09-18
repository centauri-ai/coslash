// Package fullsessionexport creates the canonical full-fidelity v2 upload
// envelope from one immutable full-session record.
package fullsessionexport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

const (
	SchemaVersion      = "session-revision/v2"
	MediaType          = "application/vnd.coslash.session-revision.v2+json"
	PreviewVersion     = "full-session-preview/v1"
	ShareVersion       = "full-session-share/v1"
	MaxRepositoryBytes = 1024
)

var ErrInvalid = errors.New("invalid full-session upload envelope")

type Repository struct {
	Canonical string `json:"canonical"`
	LocalOnly bool   `json:"localOnly"`
}

type Envelope struct {
	SchemaVersion   string               `json:"schemaVersion"`
	MediaType       string               `json:"mediaType"`
	RecordByteCount int                  `json:"recordByteCount"`
	RecordSHA256    string               `json:"recordSha256"`
	Repository      Repository           `json:"repository"`
	Record          fullsessionv1.Record `json:"record"`
}

// Marshal freezes the exact bytes accepted by the S01 v2 API. The record hash
// covers the canonical FullSessionRecord bytes, while the returned payload is
// the canonical transport envelope uploaded as one gzip request.
func Marshal(record fullsessionv1.Record, repository Repository) ([]byte, []byte, string, error) {
	if err := validateRepository(repository); err != nil {
		return nil, nil, "", err
	}
	recordBytes, err := fullsessionv1.Marshal(record)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: record: %v", ErrInvalid, err)
	}
	sum := sha256.Sum256(recordBytes)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	envelope := Envelope{
		SchemaVersion: SchemaVersion, MediaType: MediaType,
		RecordByteCount: len(recordBytes), RecordSHA256: hash,
		Repository: repository, Record: record,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: marshal: %v", ErrInvalid, err)
	}
	return payload, recordBytes, hash, nil
}

func validateRepository(repository Repository) error {
	if repository.Canonical == "" || len(repository.Canonical) > MaxRepositoryBytes ||
		!utf8.ValidString(repository.Canonical) || strings.TrimSpace(repository.Canonical) != repository.Canonical {
		return fmt.Errorf("%w: canonical repository identity is required", ErrInvalid)
	}
	return nil
}
