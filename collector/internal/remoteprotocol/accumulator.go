package remoteprotocol

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

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
	PageAfter       []PageCursor
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
	lastFamily    map[string]string
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
		PageAfter:      append([]PageCursor(nil), baseline.PageAfter...),
	}
	if copy.Families == nil {
		copy.Families = map[FamilyKey]CachedFamily{}
	}
	if copy.FullRecords == nil {
		copy.FullRecords = map[FullRecordKey]FullRecord{}
	}
	lastFamily := map[string]string{}
	for _, cursor := range request.PageAfter {
		lastFamily[cursor.Vendor] = cursor.AfterFamilyID
	}
	return &Accumulator{request: request, proposal: copy, actions: map[FamilyKey]string{}, tombstones: map[string]map[string]bool{}, completed: map[string]bool{}, lastFamily: lastFamily, nextSequence: 1}, nil
}

func (a *Accumulator) Apply(record Record) error {
	return a.ApplyEncoded(record, encodedSize(record))
}

// ApplyEncoded applies a record whose exact unframed wire size was already
// measured by a trusted local producer. This avoids remarshal of large changed
// records on the in-process SFTP path.
func (a *Accumulator) ApplyEncoded(record Record, recordSize int) error {
	if a.closed {
		return errors.New("record after request completion")
	}
	if recordSize < 0 {
		return errors.New("invalid encoded record size")
	}
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
		a.lastFamily[record.Vendor] = record.FamilyID
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
			if a.request.BaselineMode == BaselineNone {
				for familyKey := range a.proposal.Families {
					if familyKey.Vendor == record.Vendor && !inventory[familyKey.FamilyID] {
						delete(a.proposal.Families, familyKey)
						for recordKey, full := range a.proposal.FullRecords {
							if recordKey.Vendor == record.Vendor && full.FamilyID == familyKey.FamilyID {
								delete(a.proposal.FullRecords, recordKey)
							}
						}
					}
				}
			}
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
		a.updatePageCursor(record.Vendor, record.Counts.SkippedFamilies > 0)
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
	result.PageAfter = append([]PageCursor(nil), a.proposal.PageAfter...)
	return result
}

func (a *Accumulator) updatePageCursor(vendor string, limited bool) {
	cursors := a.proposal.PageAfter[:0]
	for _, cursor := range a.proposal.PageAfter {
		if cursor.Vendor != vendor {
			cursors = append(cursors, cursor)
		}
	}
	if limited && a.lastFamily[vendor] != "" {
		cursors = append(cursors, PageCursor{Vendor: vendor, AfterFamilyID: a.lastFamily[vendor]})
	}
	slices.SortFunc(cursors, func(a, b PageCursor) int { return strings.Compare(a.Vendor, b.Vendor) })
	a.proposal.PageAfter = cursors
}
