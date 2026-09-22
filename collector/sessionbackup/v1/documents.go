package sessionbackupv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

type Enrichment struct {
	Repository               *string   `json:"repository"`
	RepositoryLocalOnly      bool      `json:"repositoryLocalOnly"`
	FilesystemFallbackBranch *string   `json:"filesystemFallbackBranch"`
	Git                      *GitDrift `json:"git"`
	LastEditAtMs             *int64    `json:"lastEditAtMs"`
}

type GitDrift struct {
	BaseBranch string `json:"baseBranch"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
}

type SynthesisRecord struct {
	Agent       string                         `json:"agent"`
	SessionID   string                         `json:"sessionId"`
	Revision    int64                          `json:"mtime"`
	Model       string                         `json:"model"`
	GeneratedAt int64                          `json:"generatedAt"`
	Synthesis   fullsessionv1.SessionSynthesis `json:"synthesis"`
}

func MarshalEnrichment(document Enrichment) ([]byte, error) {
	if err := validateEnrichment(document); err != nil {
		return nil, err
	}
	return json.Marshal(document)
}

func DecodeEnrichment(data []byte) (Enrichment, error) {
	var document Enrichment
	if err := decodeCanonicalDocument(data, &document); err != nil {
		return Enrichment{}, fmt.Errorf("%w: enrichment decode: %v", ErrInvalid, err)
	}
	if err := validateEnrichment(document); err != nil {
		return Enrichment{}, err
	}
	return document, nil
}

func MarshalSynthesisRecord(record SynthesisRecord) ([]byte, error) {
	if err := validateSynthesisRecord(record); err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func DecodeSynthesisRecord(data []byte) (SynthesisRecord, error) {
	var record SynthesisRecord
	if err := decodeCanonicalDocument(data, &record); err != nil {
		return SynthesisRecord{}, fmt.Errorf("%w: synthesis decode: %v", ErrInvalid, err)
	}
	if err := validateSynthesisRecord(record); err != nil {
		return SynthesisRecord{}, err
	}
	return record, nil
}

func validateEnrichment(document Enrichment) error {
	if document.Repository != nil && (*document.Repository == "" || !plainText(*document.Repository)) {
		return fmt.Errorf("%w: invalid enrichment repository", ErrInvalid)
	}
	if document.FilesystemFallbackBranch != nil && (*document.FilesystemFallbackBranch == "" || !plainText(*document.FilesystemFallbackBranch)) {
		return fmt.Errorf("%w: invalid enrichment fallback branch", ErrInvalid)
	}
	if document.Git != nil && (document.Git.BaseBranch == "" || !plainText(document.Git.BaseBranch) || document.Git.Ahead < 0 || document.Git.Behind < 0) {
		return fmt.Errorf("%w: invalid enrichment git drift", ErrInvalid)
	}
	if document.LastEditAtMs != nil && (*document.LastEditAtMs < 0 || *document.LastEditAtMs > fullsessionv1.MaxSessionTimestampMs) {
		return fmt.Errorf("%w: invalid enrichment last-edit time", ErrInvalid)
	}
	return nil
}

func validateSynthesisRecord(record SynthesisRecord) error {
	if !identifier(record.Agent) || !identifier(record.SessionID) || record.Revision <= 0 || record.Revision > fullsessionv1.MaxSessionTimestampMs ||
		!plainText(record.Model) || record.GeneratedAt < 0 || record.GeneratedAt > fullsessionv1.MaxSessionTimestampMs ||
		!validSynthesis(record.Synthesis) {
		return fmt.Errorf("%w: invalid synthesis record", ErrInvalid)
	}
	return nil
}

func validSynthesis(value fullsessionv1.SessionSynthesis) bool {
	if len(value.Goals) > fullsessionv1.MaxItems || len(value.KeyDecisions) > fullsessionv1.MaxItems ||
		!validDocumentString(value.Outcome) || !validDocumentString(value.NextStep) {
		return false
	}
	for _, values := range [][]string{value.Goals, value.KeyDecisions} {
		for _, item := range values {
			if !validDocumentString(item) {
				return false
			}
		}
	}
	return true
}

func validDocumentString(value string) bool {
	return utf8.ValidString(value) && len(value) <= fullsessionv1.MaxStringBytes
}

func decodeCanonicalDocument(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing value")
	}
	canonical, err := json.Marshal(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, data) {
		return fmt.Errorf("non-canonical document")
	}
	return nil
}
