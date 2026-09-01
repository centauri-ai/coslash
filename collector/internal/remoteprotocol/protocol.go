// Package remoteprotocol defines and validates the bounded NDJSON collection
// protocol. It has no SSH, filesystem, or durable cache dependencies.
package remoteprotocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
)

const (
	ProtocolVersion      = 1
	MaxRequestBytes      = 256 << 10
	MaxRecordBytes       = 1 << 20
	MaxResponseBytes     = 32 << 20
	MaxRecords           = 4096
	MaxKnownFamilies     = 1024
	MaxKnownHeaders      = 2048
	MaxInventoryFamilies = 2048
)

const (
	BaselineKnown         = "known"
	BaselineNone          = "none"
	RecordHandshake       = "handshake"
	RecordChanged         = "changed_family"
	RecordUnchanged       = "unchanged_family"
	RecordSkipped         = "skipped_family"
	RecordTombstone       = "provisional_tombstone"
	RecordVendorComplete  = "vendor_complete"
	RecordRequestComplete = "request_complete"
)

type VersionRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}
type Limits struct {
	MaxRecordBytes       int `json:"max_record_bytes"`
	MaxResponseBytes     int `json:"max_response_bytes"`
	MaxRecords           int `json:"max_records"`
	MaxInventoryFamilies int `json:"max_inventory_families"`
}
type KnownFamily struct {
	Vendor      string        `json:"vendor"`
	FamilyID    string        `json:"family_id"`
	Fingerprint string        `json:"fingerprint"`
	Headers     []KnownHeader `json:"headers,omitempty"`
}
type KnownHeader struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	ModifiedAtMs int64  `json:"modified_at_ms"`
	SessionID    string `json:"session_id"`
	ParentID     string `json:"parent_id,omitempty"`
}
type Request struct {
	RequestID     string        `json:"request_id"`
	Protocol      VersionRange  `json:"protocol"`
	Schema        VersionRange  `json:"schema"`
	ParserVersion string        `json:"parser_version"`
	BaselineMode  string        `json:"baseline_mode"`
	BaselineID    string        `json:"baseline_id,omitempty"`
	SinceMs       int64         `json:"since_ms"`
	CollectedAtMs int64         `json:"collected_at_ms"`
	Vendors       []string      `json:"vendors"`
	Known         []KnownFamily `json:"known"`
	Limits        Limits        `json:"limits"`
}

// BuildRequest deterministically falls back to baseline_mode=none when the
// complete known set cannot fit. It never sends a partial known baseline.
func BuildRequest(request Request, known []KnownFamily) (Request, error) {
	known = append([]KnownFamily(nil), known...)
	headerCount := 0
	for index := range known {
		known[index].Headers = append([]KnownHeader(nil), known[index].Headers...)
		sort.Slice(known[index].Headers, func(i, j int) bool {
			return known[index].Headers[i].Key < known[index].Headers[j].Key
		})
		headerCount += len(known[index].Headers)
	}
	sort.Slice(known, func(i, j int) bool {
		if known[i].Vendor == known[j].Vendor {
			return known[i].FamilyID < known[j].FamilyID
		}
		return known[i].Vendor < known[j].Vendor
	})
	request.Known = append([]KnownFamily(nil), known...)
	request.BaselineMode = BaselineKnown
	if len(known) > MaxKnownFamilies || headerCount > MaxKnownHeaders || encodedSize(request) > MaxRequestBytes {
		request.BaselineMode, request.BaselineID, request.Known = BaselineNone, "", []KnownFamily{}
	}
	if err := ValidateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func ValidateRequest(r Request) error {
	if !validID(r.RequestID) || !validID(r.ParserVersion) {
		return errors.New("invalid request identity")
	}
	if !supports(r.Protocol, ProtocolVersion) || !supports(r.Schema, remotefacts.SchemaVersion) {
		return errors.New("unsupported version range")
	}
	if r.BaselineMode != BaselineKnown && r.BaselineMode != BaselineNone {
		return errors.New("invalid baseline_mode")
	}
	if r.BaselineMode == BaselineKnown && !validID(r.BaselineID) {
		return errors.New("known baseline requires baseline_id")
	}
	if r.BaselineMode == BaselineNone && (r.BaselineID != "" || len(r.Known) != 0) {
		return errors.New("baseline none cannot carry baseline data")
	}
	if len(r.Known) > MaxKnownFamilies || len(r.Vendors) == 0 || len(r.Vendors) > 2 {
		return errors.New("request list exceeds limit")
	}
	previousVendor := ""
	for _, vendor := range r.Vendors {
		if !validVendor(vendor) || vendor <= previousVendor {
			return errors.New("vendors must be uniquely sorted")
		}
		previousVendor = vendor
	}
	previousKnown := KnownFamily{}
	headerCount := 0
	seenHeaderKeys := map[string]bool{}
	for _, known := range r.Known {
		if !validVendor(known.Vendor) || !validID(known.FamilyID) || !validID(known.Fingerprint) || known.Vendor < previousKnown.Vendor || (known.Vendor == previousKnown.Vendor && known.FamilyID <= previousKnown.FamilyID) {
			return errors.New("known families must be valid and uniquely sorted")
		}
		previousKnown = known
		previousKey := ""
		for _, header := range known.Headers {
			if known.Vendor != "codex" || !validID(header.Key) || header.Key <= previousKey ||
				seenHeaderKeys[header.Key] ||
				header.Size < 0 || header.ModifiedAtMs < 0 ||
				header.ModifiedAtMs > remotefacts.MaxTimestampMs || !validID(header.SessionID) ||
				(header.ParentID != "" && !validID(header.ParentID)) {
				return errors.New("known headers must be valid and uniquely sorted")
			}
			previousKey = header.Key
			seenHeaderKeys[header.Key] = true
			headerCount++
		}
	}
	if headerCount > MaxKnownHeaders {
		return errors.New("known header mappings exceed limit")
	}
	if r.SinceMs < 0 || r.CollectedAtMs <= 0 || r.SinceMs > r.CollectedAtMs {
		return errors.New("invalid request window")
	}
	if r.Limits.MaxRecordBytes <= 0 || r.Limits.MaxRecordBytes > MaxRecordBytes || r.Limits.MaxResponseBytes <= 0 || r.Limits.MaxResponseBytes > MaxResponseBytes || r.Limits.MaxRecords <= 0 || r.Limits.MaxRecords > MaxRecords || r.Limits.MaxInventoryFamilies <= 0 || r.Limits.MaxInventoryFamilies > MaxInventoryFamilies {
		return errors.New("invalid requested limits")
	}
	if encodedSize(r) > MaxRequestBytes {
		return errors.New("request exceeds byte limit")
	}
	return nil
}

type Counts struct {
	CandidateFamilies int `json:"candidate_families,omitempty"`
	SelectedFamilies  int `json:"selected_families,omitempty"`
	CandidateFiles    int `json:"candidate_files,omitempty"`
	SelectedFiles     int `json:"selected_files,omitempty"`
	SkippedFamilies   int `json:"skipped_families,omitempty"`
}
type Timing struct {
	ParserMs int64 `json:"parser_ms,omitempty"`
	TotalMs  int64 `json:"total_ms,omitempty"`
}
type Record struct {
	Type                string              `json:"type"`
	ProtocolVersion     int                 `json:"protocol_version"`
	RequestID           string              `json:"request_id"`
	Sequence            int                 `json:"sequence"`
	Vendor              string              `json:"vendor,omitempty"`
	BaselineID          string              `json:"baseline_id,omitempty"`
	SchemaVersion       int                 `json:"schema_version,omitempty"`
	ParserVersion       string              `json:"parser_version,omitempty"`
	Capabilities        []string            `json:"capabilities,omitempty"`
	FamilyID            string              `json:"family_id,omitempty"`
	PriorFingerprint    string              `json:"prior_fingerprint,omitempty"`
	Fingerprint         string              `json:"fingerprint,omitempty"`
	Family              *remotefacts.Family `json:"family,omitempty"`
	Reason              string              `json:"reason,omitempty"`
	EnumerationComplete bool                `json:"enumeration_complete,omitempty"`
	InventoryComplete   bool                `json:"inventory_complete,omitempty"`
	Inventory           []string            `json:"inventory,omitempty"`
	Counts              Counts              `json:"counts,omitempty"`
	Timing              Timing              `json:"timing,omitempty"`
}

func Decode(reader io.Reader, request Request) ([]Record, error) {
	if err := ValidateRequest(request); err != nil {
		return nil, err
	}
	limited := &io.LimitedReader{R: reader, N: int64(request.Limits.MaxResponseBytes) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), request.Limits.MaxRecordBytes+1)
	records := []Record{}
	complete := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			return nil, errors.New("blank protocol record")
		}
		if len(line) > request.Limits.MaxRecordBytes {
			return nil, errors.New("record exceeds byte limit")
		}
		if complete {
			return nil, errors.New("trailing content after request_complete")
		}
		if len(records) >= request.Limits.MaxRecords {
			return nil, errors.New("record count exceeds limit")
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		var record Record
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("decode record %d: %w", len(records)+1, err)
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return nil, errors.New("trailing JSON value")
		}
		if err := validateRecord(record, request, len(records)+1); err != nil {
			return nil, err
		}
		records = append(records, record)
		complete = record.Type == RecordRequestComplete
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if limited.N <= 0 {
		return nil, errors.New("response exceeds byte limit")
	}
	return records, nil
}

func Encode(records []Record) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return nil, err
		}
	}
	return output.Bytes(), nil
}

func validateRecord(r Record, request Request, sequence int) error {
	if r.ProtocolVersion != ProtocolVersion || r.RequestID != request.RequestID || r.Sequence != sequence {
		return fmt.Errorf("record %d has mixed, replayed, or out-of-order identity", sequence)
	}
	if r.Sequence == 1 && r.Type != RecordHandshake {
		return errors.New("first record must be handshake")
	}
	switch r.Type {
	case RecordHandshake:
		if r.Sequence != 1 || r.Vendor != "" || r.BaselineID != request.BaselineID || r.SchemaVersion != remotefacts.SchemaVersion || !validID(r.ParserVersion) || len(r.Capabilities) > 32 {
			return errors.New("invalid or stale handshake")
		}
		previous := ""
		for _, capability := range r.Capabilities {
			if !validID(capability) || capability <= previous {
				return errors.New("capabilities must be uniquely sorted")
			}
			previous = capability
		}
	case RecordChanged:
		if !requestedVendor(request, r.Vendor) || !validID(r.FamilyID) || !validID(r.Fingerprint) || r.Family == nil || r.FamilyID != r.Family.FamilyID || r.Vendor != r.Family.Vendor {
			return errors.New("invalid changed family record")
		}
		if err := remotefacts.Validate(*r.Family); err != nil {
			return fmt.Errorf("invalid changed family: %w", err)
		}
	case RecordUnchanged:
		if !requestedVendor(request, r.Vendor) || !validID(r.FamilyID) || !validID(r.Fingerprint) {
			return errors.New("invalid unchanged family record")
		}
	case RecordSkipped:
		if !requestedVendor(request, r.Vendor) || !validID(r.FamilyID) || r.Reason == "" || len(r.Reason) > remotefacts.MaxDisplayBytes {
			return errors.New("invalid skipped family record")
		}
	case RecordTombstone:
		if !requestedVendor(request, r.Vendor) || !validID(r.FamilyID) {
			return errors.New("invalid tombstone")
		}
	case RecordVendorComplete:
		if !requestedVendor(request, r.Vendor) || !r.EnumerationComplete || len(r.Inventory) > request.Limits.MaxInventoryFamilies {
			return errors.New("invalid vendor completion")
		}
		if request.BaselineMode == BaselineNone && !r.InventoryComplete {
			return errors.New("baseline-free completion requires authoritative inventory")
		}
		previous := ""
		for _, id := range r.Inventory {
			if !validID(id) || id <= previous {
				return errors.New("inventory must be uniquely sorted")
			}
			previous = id
		}
	case RecordRequestComplete:
		if r.Vendor != "" {
			return errors.New("request completion cannot name a vendor")
		}
	default:
		return fmt.Errorf("unknown record type %q", r.Type)
	}
	counts := []int{r.Counts.CandidateFamilies, r.Counts.SelectedFamilies, r.Counts.CandidateFiles, r.Counts.SelectedFiles, r.Counts.SkippedFamilies}
	for _, count := range counts {
		if count < 0 || count > remotefacts.MaxCount {
			return errors.New("record count outside range")
		}
	}
	if r.Counts.SelectedFamilies > r.Counts.CandidateFamilies || r.Counts.SelectedFiles > r.Counts.CandidateFiles || r.Timing.ParserMs < 0 || r.Timing.TotalMs < 0 || r.Timing.ParserMs > r.Timing.TotalMs {
		return errors.New("inconsistent record counts or timing")
	}
	return nil
}

func supports(r VersionRange, version int) bool {
	return r.Min > 0 && r.Min <= version && r.Max >= version && r.Max >= r.Min
}
func validVendor(value string) bool { return value == "claude" || value == "codex" }
func requestedVendor(request Request, value string) bool {
	for _, vendor := range request.Vendors {
		if value == vendor {
			return true
		}
	}
	return false
}
func validID(value string) bool {
	if value == "" || len(value) > remotefacts.MaxIDBytes {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}
func encodedSize(value any) int { data, _ := json.Marshal(value); return len(data) }
