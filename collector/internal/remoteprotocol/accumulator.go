package remoteprotocol

import (
	"errors"
	"fmt"
	"maps"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
)

type FamilyKey struct{ Vendor, FamilyID string }
type FullRecordKey struct{ Vendor, SessionID string }
type CachedFamily struct {
	Facts           remotefacts.Family
	Fingerprint     string
	StaleReason     string
	LastSuccessAtMs int64
}
type Generation struct {
	SourceID        string
	BaselineID      string
	CoverageSinceMs int64
	Families        map[FamilyKey]CachedFamily
	FullRecords     map[FullRecordKey]FullRecord
	VendorComplete  map[string]bool
	RequestComplete bool
}

// Accumulator is a pure state machine. Proposal returns a copied cache
// generation; applying records never performs durable writes.
type Accumulator struct {
	request       Request
	proposal      Generation
	actions       map[FamilyKey]string
	tombstones    map[string]map[string]bool
	completed     map[string]bool
	seenHandshake bool
	nextSequence  int
	closed        bool
	records       int
	bytes         int
}

func NewAccumulator(request Request, baseline Generation) (*Accumulator, error) {
	if err := ValidateRequest(request); err != nil {
		return nil, err
	}
	if request.BaselineMode == BaselineKnown && request.BaselineID != baseline.BaselineID {
		return nil, errors.New("stale baseline")
	}
	copy := Generation{
		SourceID: request.SourceID, BaselineID: request.RequestID, CoverageSinceMs: baseline.CoverageSinceMs,
		Families: maps.Clone(baseline.Families), FullRecords: maps.Clone(baseline.FullRecords),
		VendorComplete: map[string]bool{},
	}
	if copy.Families == nil {
		copy.Families = map[FamilyKey]CachedFamily{}
	}
	if copy.FullRecords == nil {
		copy.FullRecords = map[FullRecordKey]FullRecord{}
	}
	return &Accumulator{request: request, proposal: copy, actions: map[FamilyKey]string{}, tombstones: map[string]map[string]bool{}, completed: map[string]bool{}, nextSequence: 1}, nil
}

func (a *Accumulator) Apply(record Record) error {
	if a.closed {
		return errors.New("record after request completion")
	}
	recordSize := encodedSize(record)
	size := recordSize + 1
	if recordSize > a.request.Limits.MaxRecordBytes || a.records >= a.request.Limits.MaxRecords ||
		a.bytes+size > a.request.Limits.MaxResponseBytes {
		return errors.New("response exceeds negotiated bounds")
	}
	if err := validateRecord(record, a.request, a.nextSequence); err != nil {
		return err
	}
	a.records++
	a.bytes += size
	if record.FamilyID != "" && a.completed[record.Vendor] {
		return fmt.Errorf("family action after vendor completion for %s", record.Vendor)
	}
	if record.Type == RecordRequestComplete {
		for _, vendor := range a.request.Vendors {
			if !a.completed[vendor] {
				return fmt.Errorf("request completion before vendor completion for %s", vendor)
			}
		}
	}
	a.nextSequence++
	if record.Type == RecordHandshake {
		a.seenHandshake = true
		return nil
	}
	if !a.seenHandshake {
		return errors.New("record before handshake")
	}
	key := FamilyKey{record.Vendor, record.FamilyID}
	if record.FamilyID != "" && record.Type != RecordVendorComplete {
		if prior, ok := a.actions[key]; ok {
			return fmt.Errorf("duplicate or conflicting action for %s/%s: %s then %s", key.Vendor, key.FamilyID, prior, record.Type)
		}
		a.actions[key] = record.Type
	}
	switch record.Type {
	case RecordChanged:
		current, exists := a.proposal.Families[key]
		if a.request.BaselineMode == BaselineNone {
			if record.PriorFingerprint != "" {
				return fmt.Errorf("baseline-free family %s/%s has a prior fingerprint", key.Vendor, key.FamilyID)
			}
		} else {
			if exists && record.PriorFingerprint != current.Fingerprint {
				return fmt.Errorf("stale family baseline for %s/%s", key.Vendor, key.FamilyID)
			}
			if !exists && record.PriorFingerprint != "" {
				return fmt.Errorf("new family %s/%s has a prior fingerprint", key.Vendor, key.FamilyID)
			}
		}
		a.proposal.Families[key] = CachedFamily{
			Facts: *record.Family, Fingerprint: record.Fingerprint, LastSuccessAtMs: a.request.CollectedAtMs,
		}
		for recordKey, full := range a.proposal.FullRecords {
			if recordKey.Vendor == record.Vendor && full.FamilyID == record.FamilyID {
				delete(a.proposal.FullRecords, recordKey)
			}
		}
		for _, full := range record.FullRecords {
			a.proposal.FullRecords[FullRecordKey{Vendor: record.Vendor, SessionID: full.Record.SessionID}] = full
		}
	case RecordUnchanged:
		current, ok := a.proposal.Families[key]
		if !ok || current.Fingerprint != record.Fingerprint {
			return fmt.Errorf("unchanged family %s/%s conflicts with baseline", key.Vendor, key.FamilyID)
		}
		current.StaleReason = ""
		a.proposal.Families[key] = current
	case RecordSkipped:
		if current, ok := a.proposal.Families[key]; ok {
			current.StaleReason = record.Reason
			a.proposal.Families[key] = current
		}
	case RecordTombstone:
		if a.tombstones[record.Vendor] == nil {
			a.tombstones[record.Vendor] = map[string]bool{}
		}
		a.tombstones[record.Vendor][record.FamilyID] = true
	case RecordVendorComplete:
		if a.completed[record.Vendor] {
			return fmt.Errorf("duplicate vendor completion for %s", record.Vendor)
		}
		a.completed[record.Vendor] = true
		inventory := map[string]bool{}
		for _, id := range record.Inventory {
			inventory[id] = true
		}
		if record.InventoryComplete {
			for familyID := range a.tombstones[record.Vendor] {
				if inventory[familyID] {
					return fmt.Errorf("tombstone %s/%s is present in inventory", record.Vendor, familyID)
				}
				delete(a.proposal.Families, FamilyKey{record.Vendor, familyID})
				for recordKey, full := range a.proposal.FullRecords {
					if recordKey.Vendor == record.Vendor && full.FamilyID == familyID {
						delete(a.proposal.FullRecords, recordKey)
					}
				}
			}
		}
		a.proposal.VendorComplete[record.Vendor] = record.EnumerationComplete && record.InventoryComplete
	case RecordRequestComplete:
		a.proposal.RequestComplete = true
		a.closed = true
		complete := true
		for _, vendor := range a.request.Vendors {
			complete = complete && a.proposal.VendorComplete[vendor]
		}
		if complete && (a.proposal.CoverageSinceMs == 0 || a.request.SinceMs < a.proposal.CoverageSinceMs) {
			a.proposal.CoverageSinceMs = a.request.SinceMs
		}
	}
	return nil
}

func (a *Accumulator) Proposal() Generation {
	result := a.proposal
	result.Families = maps.Clone(a.proposal.Families)
	result.FullRecords = maps.Clone(a.proposal.FullRecords)
	result.VendorComplete = maps.Clone(a.proposal.VendorComplete)
	return result
}
