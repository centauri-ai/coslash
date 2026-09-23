package remotehelper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

// Options are the helper's run inputs. Nothing here comes from the request: the
// home directory is the SSH user's own, and the limits are helper-owned.
type Options struct {
	Home              string
	Now               func() time.Time
	Limits            Limits
	Deadline          time.Duration
	ProcessAlive      func(int) bool
	CodexLiveSessions func() (map[string]struct{}, error)
}

// Outcome reports what the response contained. RequestComplete is false when a
// vendor could not be enumerated authoritatively, which leaves the refresh
// partial without discarding the families that did arrive.
type Outcome struct {
	Records         int
	Bytes           int
	VendorsComplete []string
	RequestComplete bool
}

// Collect answers one validated request. It streams records as they are produced
// and returns an error only when the response itself cannot continue.
func Collect(
	ctx context.Context,
	request remoteprotocol.Request,
	options Options,
	output io.Writer,
) (Outcome, error) {
	if err := remoteprotocol.ValidateRequest(request); err != nil {
		return Outcome{}, fmt.Errorf("invalid request: %w", err)
	}
	home := options.Home
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil {
			return Outcome{}, fmt.Errorf("resolve home directory: %w", err)
		}
		home = resolved
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	// Liveness is the one probe outside the file allowlist: signal 0 against a
	// PID the metadata files name, never a path from the request.
	alive := options.ProcessAlive
	if alive == nil {
		alive = session.IsProcessAlive
	}
	codexLiveSessions := options.CodexLiveSessions
	if codexLiveSessions == nil {
		codexLiveSessions = codex.LoadLiveSessions
	}
	deadline := options.Deadline
	if deadline <= 0 {
		deadline = CollectDeadline
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	source, err := OpenSource(home, options.Limits)
	if err != nil {
		return Outcome{}, err
	}
	defer source.Close()

	started := time.Now()
	emitter := newEmitter(output, request)
	if err := emitter.handshake(); err != nil {
		return outcomeOf(emitter, nil, false), err
	}
	completed := []string{}
	parserTotal := time.Duration(0)
	totals := remoteprotocol.Counts{}
	for index, vendor := range request.Vendors {
		if ctx.Err() != nil {
			break
		}
		result, err := collectVendor(
			ctx, emitter, request, vendor, request.Vendors[index+1:], source, home, now(), alive, codexLiveSessions,
		)
		parserTotal += result.parser
		addCounts(&totals, result.counts)
		if err != nil {
			return outcomeOf(emitter, completed, false), err
		}
		if result.complete {
			completed = append(completed, vendor)
		}
	}
	if len(completed) != len(request.Vendors) {
		return outcomeOf(emitter, completed, false), nil
	}
	total := time.Since(started)
	err = emitter.emit(remoteprotocol.Record{
		Type:   remoteprotocol.RecordRequestComplete,
		Counts: totals,
		Timing: timing(parserTotal, total),
	})
	if err != nil {
		return outcomeOf(emitter, completed, false), err
	}
	return outcomeOf(emitter, completed, true), nil
}

func collectVendor(
	ctx context.Context,
	emitter *emitter,
	request remoteprotocol.Request,
	vendor string,
	remainingVendors []string,
	source *Source,
	home string,
	now time.Time,
	alive func(int) bool,
	codexLiveSessions func() (map[string]struct{}, error),
) (vendorResult, error) {
	started := time.Now()
	scanned := scanVendor(vendor, source, home, request, now, alive, codexLiveSessions)
	if scanned == nil {
		return vendorResult{}, nil
	}
	known := knownFamilies(request, vendor)
	baselineKnown := request.BaselineMode == remoteprotocol.BaselineKnown

	changed := []*family{}
	actions := []remoteprotocol.Record{}
	counts := remoteprotocol.Counts{
		CandidateFamilies: len(scanned.scan.families),
		CandidateFiles:    scanned.scan.candidateFiles,
		SelectedFiles:     scanned.scan.selectedFiles,
	}
	for _, item := range pageFamilies(scanned.scan.sortedFamilies(), pageAfter(request, vendor)) {
		cached, isKnown := known[item.id]
		switch {
		case item.skipReason != "":
			counts.SkippedFamilies++
			if !isKnown {
				continue
			}
			actions = append(actions, remoteprotocol.Record{
				Type: remoteprotocol.RecordSkipped, Vendor: vendor,
				FamilyID: item.id, Reason: item.skipReason,
			})
		case baselineKnown && isKnown && cached == item.fingerprint:
			actions = append(actions, remoteprotocol.Record{
				Type: remoteprotocol.RecordUnchanged, Vendor: vendor,
				FamilyID: item.id, Fingerprint: item.fingerprint,
			})
		case !item.inWindow:
			// Outside the requested window a family is neither confirmed nor
			// replaced. Its absence from the response is never deletion, and the
			// inventory still proves it exists.
			continue
		default:
			changed = append(changed, item)
		}
	}

	preliminaryInventory, _ := scanned.scan.inventory(request.Limits.MaxInventoryFamilies)
	potentialTombstones := potentialTombstoneIDs(scanned, changed, known)
	reservation, reserveErr := completionReserve(
		emitter, vendor, preliminaryInventory, potentialTombstones, remainingVendors,
	)
	if reserveErr != nil {
		return vendorResult{counts: counts}, reserveErr
	}
	emitter.reservedBytes = reservation.bytes
	emitter.reservedRecords = reservation.records
	budgetBefore := emitter.budgetSkipped
	for _, action := range actions {
		if action.Type == remoteprotocol.RecordSkipped {
			if actionErr := emitSkipped(emitter, action.Vendor, action.FamilyID, action.Reason); actionErr != nil {
				return vendorResult{counts: counts}, actionErr
			}
			continue
		}
		emitted, actionErr := emitter.emitFamily(action, &counts)
		if actionErr != nil {
			return vendorResult{counts: counts}, actionErr
		}
		if emitted && action.Type == remoteprotocol.RecordUnchanged {
			counts.SelectedFamilies++
		}
	}
	parser, err := publishChanged(ctx, emitter, request, scanned, changed, known, &counts)
	budgetSkipped := emitter.budgetSkipped - budgetBefore
	result := vendorResult{parser: parser, counts: counts}
	if err != nil {
		return result, err
	}

	if baselineKnown && scanned.scan.complete {
		for _, id := range sortedKeys(known) {
			if _, exists := scanned.scan.families[id]; exists {
				continue
			}
			err := emitter.emit(remoteprotocol.Record{
				Type: remoteprotocol.RecordTombstone, Vendor: vendor, FamilyID: id,
			})
			if err != nil {
				return result, err
			}
		}
	}

	inventory, inventoryComplete := scanned.scan.inventory(request.Limits.MaxInventoryFamilies)

	// vendor_complete asserts authoritative enumeration, so it is emitted only
	// when the scan really saw everything. A baseline-free response must also
	// carry the complete inventory or it cannot authorise any deletion.
	if !scanned.scan.complete || counts.SkippedFamilies > budgetSkipped || ctx.Err() != nil {
		return result, nil
	}
	if request.BaselineMode == remoteprotocol.BaselineNone && !inventoryComplete {
		return result, nil
	}
	err = emitter.emit(remoteprotocol.Record{
		Type: remoteprotocol.RecordVendorComplete, Vendor: vendor,
		EnumerationComplete: true, InventoryComplete: inventoryComplete,
		Inventory: inventory, Counts: counts,
		Timing: timing(parser, time.Since(started)),
	})
	if err != nil {
		return result, err
	}
	result.complete = true
	return result, nil
}

func pageAfter(request remoteprotocol.Request, vendor string) string {
	for _, cursor := range request.PageAfter {
		if cursor.Vendor == vendor {
			return cursor.AfterFamilyID
		}
	}
	return ""
}

// pageFamilies starts just after the last family committed by the prior
// limited page and wraps at the end. Circular ordering keeps later families
// from starving while still revisiting earlier families on subsequent pages.
func pageFamilies(families []*family, after string) []*family {
	if after == "" || len(families) == 0 {
		return families
	}
	index := sort.Search(len(families), func(i int) bool { return families[i].id > after })
	result := make([]*family, 0, len(families))
	result = append(result, families[index:]...)
	result = append(result, families[:index]...)
	return result
}

// vendorResult is what one vendor contributed: whether it could be enumerated
// authoritatively, how long parsing took, and its coverage counts.
type vendorResult struct {
	complete bool
	parser   time.Duration
	counts   remoteprotocol.Counts
}

func addCounts(total *remoteprotocol.Counts, item remoteprotocol.Counts) {
	total.CandidateFamilies += item.CandidateFamilies
	total.SelectedFamilies += item.SelectedFamilies
	total.CandidateFiles += item.CandidateFiles
	total.SelectedFiles += item.SelectedFiles
	total.SkippedFamilies += item.SkippedFamilies
}

// publishChanged parses the changed families and emits one record each. A parse
// failure is isolated to its own family, and a family whose files moved under
// the parser is retried before it is reported unstable.
func publishChanged(
	ctx context.Context,
	emitter *emitter,
	request remoteprotocol.Request,
	scanned *vendorScan,
	changed []*family,
	known map[string]string,
	counts *remoteprotocol.Counts,
) (time.Duration, error) {
	if len(changed) == 0 {
		return 0, nil
	}
	parser := time.Duration(0)
	started := time.Now()
	parsed, err := scanned.parse(familyFiles(changed))
	parser += time.Since(started)
	byFamily := map[string][]*vendors.ParsedSession{}
	if err == nil {
		byFamily = groupByFamily(parsed, changed)
	}
	for _, item := range changed {
		if ctx.Err() != nil {
			return parser, nil
		}
		sessions := byFamily[item.id]
		if err != nil {
			// The batch could not be trusted, so this family is parsed alone.
			retryStarted := time.Now()
			single, singleErr := scanned.parse(item.files)
			parser += time.Since(retryStarted)
			if singleErr != nil {
				if skipErr := emitSkipped(
					emitter, scanned.vendor, item.id,
					boundedReason(singleErr),
				); skipErr != nil {
					return parser, skipErr
				}
				counts.SkippedFamilies++
				continue
			}
			sessions = groupByFamily(single, []*family{item})[item.id]
		}
		spent, err := publishFamily(emitter, request, scanned, item, sessions, known, counts)
		parser += spent
		if err != nil {
			return parser, err
		}
	}
	return parser, nil
}

func publishFamily(
	emitter *emitter,
	request remoteprotocol.Request,
	scanned *vendorScan,
	item *family,
	sessions []*vendors.ParsedSession,
	known map[string]string,
	counts *remoteprotocol.Counts,
) (time.Duration, error) {
	parser := time.Duration(0)
	for attempt := 0; ; attempt++ {
		if len(sessions) == 0 {
			return parser, skipFamily(
				emitter, scanned, item, counts, remotefacts.StaleReasonNoData,
			)
		}
		if stable, reason := restabilize(scanned, item); !stable {
			if attempt >= UnstableRetries {
				return parser, skipFamily(emitter, scanned, item, counts, reason)
			}
			started := time.Now()
			reparsed, err := scanned.parse(item.files)
			parser += time.Since(started)
			if err != nil {
				return parser, skipFamily(
					emitter, scanned, item, counts,
					boundedReason(err),
				)
			}
			sessions = groupByFamily(reparsed, []*family{item})[item.id]
			continue
		}
		var complete []fullsessionv1.Record
		if scanned.vendor == vendors.AgentCodex && request.SourceID != "" {
			var fullErr error
			complete, fullErr = fullsessionrecord.FromParsedFamily(request.SourceID, scanned.vendor, scanned.source, sessions, scanned.metadata)
			if fullErr != nil {
				return parser, skipFamily(emitter, scanned, item, counts, remotefacts.StaleReasonInvalidData)
			}
			if !containsFullRecord(complete, item.id) {
				delete(scanned.scan.families, item.id)
				return parser, nil
			}
		} else if !fullsessionrecord.IsServableFamily(item.id, scanned.vendor, scanned.source, sessions, scanned.metadata) {
			delete(scanned.scan.families, item.id)
			return parser, nil
		}
		facts, err := familyFacts(scanned, item, sessions)
		if err != nil {
			return parser, skipFamily(
				emitter, scanned, item, counts,
				boundedReason(err),
			)
		}
		var fullRecords []remoteprotocol.FullRecord
		if scanned.vendor == vendors.AgentCodex && request.SourceID != "" {
			for _, completeRecord := range complete {
				fullRecords = append(fullRecords, remoteprotocol.FullRecord{FamilyID: item.id, Record: completeRecord})
			}
		}
		// A baseline-free response carries no prior fingerprint: the helper was
		// given no comparison state, and the inventory is the deletion authority.
		prior := ""
		if request.BaselineMode == remoteprotocol.BaselineKnown {
			prior = known[item.id]
		}
		changedRecord := remoteprotocol.Record{
			Type: remoteprotocol.RecordChanged, Vendor: scanned.vendor, FamilyID: item.id,
			PriorFingerprint: prior, Fingerprint: item.fingerprint, Family: &facts, FullRecords: fullRecords,
		}
		line, fits, fitErr := emitter.prepareBounded(changedRecord)
		if fitErr != nil {
			return parser, fitErr
		}
		if !fits {
			return parser, skipFamily(emitter, scanned, item, counts, remotefacts.StaleReasonVendorBudgetExceeded)
		}
		if !emitter.fitsPrepared(line, emitter.reservedBytes) {
			// The family is valid on its own, but emitting it would prevent a
			// publishable limited response. Omit it until the next baseline.
			counts.SkippedFamilies++
			emitter.budgetSkipped++
			return parser, nil
		}
		emitErr := emitter.emitPrepared(changedRecord, line)
		if emitErr != nil {
			return parser, emitErr
		}
		counts.SelectedFamilies++
		return parser, nil
	}
}

// completionReserve bounds the final vendor and request records using the
// largest valid numeric fields and sequence. Actual completion records can
// therefore never grow beyond the bytes kept aside for them.
type completionReservation struct {
	bytes   int
	records int
}

func completionReserve(emitter *emitter, vendor string, inventory, tombstones, remainingVendors []string) (completionReservation, error) {
	counts := remoteprotocol.Counts{
		CandidateFamilies: remotefacts.MaxCount, SelectedFamilies: remotefacts.MaxCount,
		CandidateFiles: remotefacts.MaxCount, SelectedFiles: remotefacts.MaxCount,
		SkippedFamilies: remotefacts.MaxCount,
	}
	timing := remoteprotocol.Timing{ParserMs: math.MaxInt64, TotalMs: math.MaxInt64}
	records := make([]remoteprotocol.Record, 0, len(tombstones)+2)
	for _, id := range tombstones {
		records = append(records, remoteprotocol.Record{
			Type: remoteprotocol.RecordTombstone, Vendor: vendor, FamilyID: id,
		})
	}
	records = append(records,
		remoteprotocol.Record{Type: remoteprotocol.RecordVendorComplete, Vendor: vendor, EnumerationComplete: true,
			InventoryComplete: true, Inventory: inventory, Counts: counts, Timing: timing},
	)
	maxInventory := make([]string, emitter.request.Limits.MaxInventoryFamilies)
	for index := range maxInventory {
		maxInventory[index] = fmt.Sprintf("%0*d", remotefacts.MaxIDBytes, index)
	}
	for _, futureVendor := range remainingVendors {
		for familyID := range knownFamilies(emitter.request, futureVendor) {
			records = append(records, remoteprotocol.Record{
				Type: remoteprotocol.RecordTombstone, Vendor: futureVendor, FamilyID: familyID,
			})
		}
		records = append(records, remoteprotocol.Record{
			Type: remoteprotocol.RecordVendorComplete, Vendor: futureVendor,
			EnumerationComplete: true, InventoryComplete: true,
			Inventory: maxInventory, Counts: counts, Timing: timing,
		})
	}
	records = append(records, remoteprotocol.Record{Type: remoteprotocol.RecordRequestComplete, Counts: counts, Timing: timing})
	total := 0
	for _, record := range records {
		record.ProtocolVersion = remoteprotocol.ProtocolVersion
		record.RequestID = emitter.request.RequestID
		record.Sequence = emitter.request.Limits.MaxRecords
		line, err := marshalRecord(record)
		if err != nil {
			return completionReservation{}, err
		}
		total += len(line)
	}
	return completionReservation{bytes: total, records: len(records)}, nil
}

func potentialTombstoneIDs(scanned *vendorScan, changed []*family, known map[string]string) []string {
	potential := map[string]bool{}
	for id := range known {
		if _, exists := scanned.scan.families[id]; !exists {
			potential[id] = true
		}
	}
	for _, item := range changed {
		if _, exists := known[item.id]; exists {
			// Parsing may prove a scanned family unservable and remove it.
			potential[item.id] = true
		}
	}
	ids := make([]string, 0, len(potential))
	for id := range potential {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func containsFullRecord(records []fullsessionv1.Record, sessionID string) bool {
	for _, record := range records {
		if record.SessionID == sessionID {
			return true
		}
	}
	return false
}

// familyFacts assembles one rooted family. Membership comes from the grouping
// pass, so the family ID the Mac caches always matches the ID the inventory
// proves exists.
func familyFacts(
	scanned *vendorScan,
	item *family,
	sessions []*vendors.ParsedSession,
) (remotefacts.Family, error) {
	present := map[string]bool{}
	for _, parsed := range sessions {
		present[parsed.Session.ID] = true
	}
	if !present[item.id] {
		return remotefacts.Family{}, fmt.Errorf("%w: family root transcript is unavailable", vendors.ErrInvalidData)
	}
	state := remotefacts.StateComplete
	if len(sessions) < len(item.sessionIDs) {
		state = remotefacts.StatePartial
	}
	family, err := remotefacts.FromParsed(
		scanned.vendor, item.id, vendors.ParserVersion, state, "",
		sessions, scanned.metadata, item.fingerprints, item.headerMappings,
	)
	if err != nil {
		return remotefacts.Family{}, fmt.Errorf("%w: %v", vendors.ErrInvalidData, err)
	}
	return family, nil
}

// restabilize re-stats a family's files after the parse. Fingerprint equality is
// an optimisation, not proof of immutability, so a file that moved under the
// parser invalidates the facts just produced.
func restabilize(scanned *vendorScan, item *family) (bool, string) {
	if item.identityUnstable {
		return false, remotefacts.StaleReasonUnstableFile
	}
	stable := true
	for _, file := range item.files {
		before, ok := scanned.fileFacts[file]
		if !ok {
			return false, remotefacts.StaleReasonUnstableFile
		}
		info, err := scanned.statFile(file)
		if err != nil {
			return false, remotefacts.StaleReasonUnstableFile
		}
		refreshed := sftpCompatibleFingerprint(vendors.FileFingerprint{
			Key: before.Key, Size: info.Size(), ModifiedAtMs: info.ModTime().UnixMilli(),
		})
		if refreshed.Size != before.Size || refreshed.ModifiedAtMs != before.ModifiedAtMs {
			stable = false
			scanned.fileFacts[file] = refreshed
			for index := range item.fingerprints {
				if item.fingerprints[index].Key == refreshed.Key {
					item.fingerprints[index] = refreshed
				}
			}
		}
	}
	if stable {
		return true, ""
	}
	if scanned.vendor == vendors.AgentCodex && !codexHeadersStillMatch(scanned, item) {
		item.identityUnstable = true
		return false, remotefacts.StaleReasonUnstableFile
	}
	item.fingerprint = aggregateFingerprint(item, scanned.metadata)
	return false, remotefacts.StaleReasonUnstableFile
}

func codexHeadersStillMatch(scanned *vendorScan, item *family) bool {
	expected := make(map[string]remotefacts.HeaderMapping, len(item.headerMappings))
	for _, mapping := range item.headerMappings {
		expected[mapping.Key] = mapping
	}
	for file, header := range codex.HeadersSource(scanned.source, item.files) {
		fingerprint, ok := scanned.fileFacts[file]
		mapping, mapped := expected[fingerprint.Key]
		if !ok || !mapped || header.Err != nil || mapping.SessionID != header.SessionID ||
			mapping.ParentID != header.ParentID {
			return false
		}
	}
	return true
}

func skipFamily(
	emitter *emitter,
	scanned *vendorScan,
	item *family,
	counts *remoteprotocol.Counts,
	reason string,
) error {
	if err := emitSkipped(emitter, scanned.vendor, item.id, reason); err != nil {
		return err
	}
	counts.SkippedFamilies++
	return nil
}

func emitSkipped(emitter *emitter, vendor, familyID, reason string) error {
	record := remoteprotocol.Record{
		Type: remoteprotocol.RecordSkipped, Vendor: vendor,
		FamilyID: familyID, Reason: reason,
	}
	line, fits, err := emitter.prepareBounded(record)
	if err != nil {
		return err
	}
	if !fits {
		return fmt.Errorf("%w: skipped_family record", ErrRecordLimit)
	}
	if !emitter.fitsPrepared(line, emitter.reservedBytes) {
		// The family was already counted as a real parse/scan skip, which by
		// itself withholds completion. Omitting its diagnostic action protects
		// the reserved completion envelope without reclassifying that failure
		// as a budget-only omission.
		return nil
	}
	return emitter.emitPrepared(record, line)
}

// groupByFamily maps parsed sessions onto the families the grouping pass built.
// A session the grouping pass never saw belongs to no requested family and is
// dropped rather than published under a guessed identity.
func groupByFamily(
	parsed []*vendors.ParsedSession,
	families []*family,
) map[string][]*vendors.ParsedSession {
	owner := map[string]string{}
	for _, item := range families {
		for _, id := range item.sessionIDs {
			owner[id] = item.id
		}
	}
	result := map[string][]*vendors.ParsedSession{}
	for _, item := range parsed {
		if item == nil || item.Session == nil {
			continue
		}
		familyID, ok := owner[item.Session.ID]
		if !ok {
			continue
		}
		result[familyID] = append(result[familyID], item)
	}
	return result
}

func scanVendor(
	vendor string,
	source *Source,
	home string,
	request remoteprotocol.Request,
	now time.Time,
	alive func(int) bool,
	codexLiveSessions func() (map[string]struct{}, error),
) *vendorScan {
	switch vendor {
	case vendors.AgentClaude:
		return scanClaude(source, home, request.SinceMs, now, alive)
	case vendors.AgentCodex:
		return scanCodex(source, home, request, codexLiveSessions)
	default:
		return nil
	}
}

func knownFamilies(request remoteprotocol.Request, vendor string) map[string]string {
	known := map[string]string{}
	if request.BaselineMode != remoteprotocol.BaselineKnown {
		return known
	}
	for _, item := range request.Known {
		if item.Vendor == vendor {
			known[item.FamilyID] = item.Fingerprint
		}
	}
	return known
}

func familyFiles(families []*family) []string {
	files := []string{}
	for _, item := range families {
		files = append(files, item.files...)
	}
	sort.Strings(files)
	return files
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func timing(parser, total time.Duration) remoteprotocol.Timing {
	parserMs := parser.Milliseconds()
	totalMs := total.Milliseconds()
	if parserMs > totalMs {
		parserMs = totalMs
	}
	return remoteprotocol.Timing{ParserMs: parserMs, TotalMs: totalMs}
}

func outcomeOf(emitter *emitter, completed []string, requestComplete bool) Outcome {
	return Outcome{
		Records: emitter.records, Bytes: emitter.bytes,
		VendorsComplete: completed, RequestComplete: requestComplete,
	}
}

// boundedReason converts filesystem and parser failures to fixed codes. Skip
// reasons are persisted in the Mac cache, so even bounded remote error prose
// must never cross the helper boundary.
func boundedReason(err error) string {
	switch {
	case errors.Is(err, ErrFileLimit):
		return remotefacts.StaleReasonOversizedFile
	case errors.Is(err, ErrEntryLimit), errors.Is(err, ErrDepthLimit):
		return remotefacts.StaleReasonVendorBudgetExceeded
	case errors.Is(err, ErrSymlink):
		return remotefacts.StaleReasonPathDenied
	case errors.Is(err, ErrPathDenied):
		return remotefacts.StaleReasonPathDenied
	case errors.Is(err, vendors.ErrInvalidData):
		return remotefacts.StaleReasonInvalidData
	case errors.Is(err, os.ErrNotExist):
		return remotefacts.StaleReasonUnstableFile
	case errors.Is(err, os.ErrPermission):
		return remotefacts.StaleReasonReadFailed
	default:
		return remotefacts.StaleReasonReadFailed
	}
}
