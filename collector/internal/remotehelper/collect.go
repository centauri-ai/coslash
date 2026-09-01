package remotehelper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Options are the helper's run inputs. Nothing here comes from the request: the
// home directory is the SSH user's own, and the limits are helper-owned.
type Options struct {
	Home         string
	Now          func() time.Time
	Limits       Limits
	Deadline     time.Duration
	ProcessAlive func(int) bool
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
	for _, vendor := range request.Vendors {
		if ctx.Err() != nil {
			break
		}
		result, err := collectVendor(
			ctx, emitter, request, vendor, source, home, now(), alive,
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
	source *Source,
	home string,
	now time.Time,
	alive func(int) bool,
) (vendorResult, error) {
	started := time.Now()
	scanned := scanVendor(vendor, source, home, request.SinceMs, now, alive)
	if scanned == nil {
		return vendorResult{}, nil
	}
	known := knownFamilies(request, vendor)
	baselineKnown := request.BaselineMode == remoteprotocol.BaselineKnown

	changed := []*family{}
	counts := remoteprotocol.Counts{
		CandidateFamilies: len(scanned.scan.families),
		CandidateFiles:    scanned.scan.candidateFiles,
		SelectedFiles:     scanned.scan.selectedFiles,
	}
	for _, item := range scanned.scan.sortedFamilies() {
		cached, isKnown := known[item.id]
		switch {
		case item.skipReason != "":
			if !isKnown {
				continue
			}
			if err := emitSkipped(emitter, vendor, item.id, item.skipReason); err != nil {
				return vendorResult{counts: counts}, err
			}
			counts.SkippedFamilies++
		case baselineKnown && isKnown && cached == item.fingerprint:
			err := emitter.emit(remoteprotocol.Record{
				Type: remoteprotocol.RecordUnchanged, Vendor: vendor,
				FamilyID: item.id, Fingerprint: item.fingerprint,
			})
			if err != nil {
				return vendorResult{counts: counts}, err
			}
			counts.SelectedFamilies++
		case !item.inWindow:
			// Outside the requested window a family is neither confirmed nor
			// replaced. Its absence from the response is never deletion, and the
			// inventory still proves it exists.
			continue
		default:
			changed = append(changed, item)
		}
	}

	parser, err := publishChanged(ctx, emitter, request, scanned, changed, known, &counts)
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
	if !scanned.scan.complete || ctx.Err() != nil {
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
					boundedReason("transcript could not be parsed", singleErr),
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
				emitter, scanned, item, counts, "no sessions could be parsed for this family",
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
					boundedReason("transcript could not be parsed", err),
				)
			}
			sessions = groupByFamily(reparsed, []*family{item})[item.id]
			continue
		}
		facts, err := familyFacts(scanned, item, sessions)
		if err != nil {
			return parser, skipFamily(
				emitter, scanned, item, counts,
				boundedReason("family facts failed validation", err),
			)
		}
		// A baseline-free response carries no prior fingerprint: the helper was
		// given no comparison state, and the inventory is the deletion authority.
		prior := ""
		if request.BaselineMode == remoteprotocol.BaselineKnown {
			prior = known[item.id]
		}
		emitErr := emitter.emit(remoteprotocol.Record{
			Type: remoteprotocol.RecordChanged, Vendor: scanned.vendor, FamilyID: item.id,
			PriorFingerprint: prior, Fingerprint: item.fingerprint, Family: &facts,
		})
		if emitErr != nil {
			return parser, emitErr
		}
		counts.SelectedFamilies++
		return parser, nil
	}
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
		return remotefacts.Family{}, errors.New("family root transcript is unavailable")
	}
	state := remotefacts.StateComplete
	if len(sessions) < len(item.sessionIDs) {
		state = remotefacts.StatePartial
	}
	for _, parsed := range sessions {
		if parsed.Session.ID == item.id {
			// Within its family the root has no parent, whatever a header claimed.
			parsed.ParentID, parsed.SpawnKey = "", ""
			continue
		}
		if parsed.ParentID == "" || !present[parsed.ParentID] {
			// The linking transcript is missing this generation; keep the session
			// on its own card rather than dropping collected facts.
			parsed.ParentID = item.id
			state = remotefacts.StatePartial
		}
	}
	return remotefacts.FromParsed(
		scanned.vendor, item.id, vendors.ParserVersion, state, "",
		sessions, scanned.metadata, item.fingerprints,
	)
}

// restabilize re-stats a family's files after the parse. Fingerprint equality is
// an optimisation, not proof of immutability, so a file that moved under the
// parser invalidates the facts just produced.
func restabilize(scanned *vendorScan, item *family) (bool, string) {
	stable := true
	for _, file := range item.files {
		before, ok := scanned.fileFacts[file]
		if !ok {
			return false, "transcript disappeared while it was read"
		}
		info, err := scanned.statFile(file)
		if err != nil {
			return false, "transcript disappeared while it was read"
		}
		if info.Size() != before.Size || info.ModTime().UnixMilli() != before.ModifiedAtMs {
			stable = false
			refreshed := before
			refreshed.Size = info.Size()
			refreshed.ModifiedAtMs = info.ModTime().UnixMilli()
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
	item.fingerprint = aggregateFingerprint(item, scanned.metadata)
	return false, "transcript changed while it was read"
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
	return emitter.emit(remoteprotocol.Record{
		Type: remoteprotocol.RecordSkipped, Vendor: vendor,
		FamilyID: familyID, Reason: reason,
	})
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
	since int64,
	now time.Time,
	alive func(int) bool,
) *vendorScan {
	switch vendor {
	case vendors.AgentClaude:
		return scanClaude(source, home, since, now, alive)
	case vendors.AgentCodex:
		return scanCodex(source, home, since)
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

// boundedReason keeps a structured skip reason inside the schema's display bound
// and never repeats a path or transcript content back to the Mac.
func boundedReason(reason string, err error) string {
	if err == nil {
		return reason
	}
	switch {
	case errors.Is(err, ErrFileLimit):
		return reason + ": file exceeds the size limit"
	case errors.Is(err, ErrEntryLimit), errors.Is(err, ErrDepthLimit):
		return reason + ": safety limit reached"
	case errors.Is(err, ErrSymlink):
		return reason + ": symlinks are not followed"
	case errors.Is(err, ErrPathDenied):
		return reason + ": path is outside the read allowlist"
	case errors.Is(err, vendors.ErrInvalidData):
		return reason + ": transcript data is invalid"
	case errors.Is(err, os.ErrNotExist):
		return reason + ": file no longer exists"
	case errors.Is(err, os.ErrPermission):
		return reason + ": file is not readable"
	default:
		return reason
	}
}
