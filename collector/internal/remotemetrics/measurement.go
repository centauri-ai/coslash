// Package remotemetrics records privacy-safe collection costs. Reports contain
// numeric counts and environment labels only, never paths, IDs, or content.
package remotemetrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const ManifestVersion = 1

type Limits struct {
	MaxFileBytes  int64 `json:"max_file_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
	MaxFamilies   int   `json:"max_families"`
	MaxDurationMs int64 `json:"max_duration_ms"`
}

type Observation struct {
	Scenario           string `json:"scenario"`
	Vendor             string `json:"vendor"`
	CandidateFamilies  int    `json:"candidate_families"`
	SelectedFamilies   int    `json:"selected_families"`
	CandidateFiles     int    `json:"candidate_files"`
	SelectedFiles      int    `json:"selected_files"`
	MetadataBytes      int64  `json:"metadata_bytes"`
	HeaderBytes        int64  `json:"header_bytes"`
	BodyBytes          int64  `json:"body_bytes"`
	MetadataOperations int    `json:"metadata_operations"`
	HeaderOperations   int    `json:"header_operations"`
	BodyOperations     int    `json:"body_operations"`
	ParserMs           int64  `json:"parser_ms"`
	TotalMs            int64  `json:"total_ms"`
	FirstResultMs      int64  `json:"first_result_ms"`
	RequestBytes       int    `json:"request_bytes"`
	ResponseBytes      int    `json:"response_bytes"`
	Partial            bool   `json:"partial"`
}

type Manifest struct {
	Version          int           `json:"version"`
	Build            string        `json:"build"`
	Hardware         string        `json:"hardware"`
	Filesystem       string        `json:"filesystem"`
	SSHRTTMs         float64       `json:"ssh_rtt_ms"`
	RequestedSinceMs int64         `json:"requested_since_ms"`
	CollectedAtMs    int64         `json:"collected_at_ms"`
	Limits           Limits        `json:"limits"`
	Observations     []Observation `json:"observations"`
}

type Totals struct {
	Observations         int   `json:"observations"`
	MetadataBytes        int64 `json:"metadata_bytes"`
	HeaderBytes          int64 `json:"header_bytes"`
	BodyBytes            int64 `json:"body_bytes"`
	Operations           int   `json:"operations"`
	ResponseBytes        int   `json:"response_bytes"`
	MaximumTotalMs       int64 `json:"maximum_total_ms"`
	MaximumFirstResultMs int64 `json:"maximum_first_result_ms"`
}

func Read(reader io.Reader) (Manifest, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("trailing manifest content")
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Validate(m Manifest) error {
	if m.Version != ManifestVersion || m.Build == "" || m.Hardware == "" || m.Filesystem == "" {
		return errors.New("missing measurement environment")
	}
	if m.SSHRTTMs < 0 || m.RequestedSinceMs < 0 || m.CollectedAtMs <= 0 || m.RequestedSinceMs > m.CollectedAtMs {
		return errors.New("invalid measurement timing")
	}
	if len(m.Observations) == 0 || len(m.Observations) > 1000 {
		return errors.New("invalid observation count")
	}
	for i, o := range m.Observations {
		if o.Scenario == "" || (o.Vendor != "claude" && o.Vendor != "codex") {
			return fmt.Errorf("observation %d has invalid identity", i)
		}
		values := []int64{int64(o.CandidateFamilies), int64(o.SelectedFamilies), int64(o.CandidateFiles), int64(o.SelectedFiles), o.MetadataBytes, o.HeaderBytes, o.BodyBytes, int64(o.MetadataOperations), int64(o.HeaderOperations), int64(o.BodyOperations), o.ParserMs, o.TotalMs, o.FirstResultMs, int64(o.RequestBytes), int64(o.ResponseBytes)}
		for _, value := range values {
			if value < 0 {
				return fmt.Errorf("observation %d has negative metric", i)
			}
		}
		if o.SelectedFamilies > o.CandidateFamilies || o.SelectedFiles > o.CandidateFiles || o.ParserMs > o.TotalMs || o.FirstResultMs > o.TotalMs {
			return fmt.Errorf("observation %d has inconsistent metrics", i)
		}
	}
	return nil
}

func Summarize(m Manifest) (Totals, error) {
	if err := Validate(m); err != nil {
		return Totals{}, err
	}
	t := Totals{Observations: len(m.Observations)}
	for _, o := range m.Observations {
		t.MetadataBytes += o.MetadataBytes
		t.HeaderBytes += o.HeaderBytes
		t.BodyBytes += o.BodyBytes
		t.Operations += o.MetadataOperations + o.HeaderOperations + o.BodyOperations
		t.ResponseBytes += o.ResponseBytes
		t.MaximumTotalMs = max(t.MaximumTotalMs, o.TotalMs)
		t.MaximumFirstResultMs = max(t.MaximumFirstResultMs, o.FirstResultMs)
	}
	return t, nil
}
