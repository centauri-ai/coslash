package remote

import (
	"errors"
	"fmt"
	"sort"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

// Incremental SFTP collection drives the same pure remoteprotocol.Accumulator
// state machine a Linux helper's NDJSON response would drive: each vendor is
// diffed against the cached baseline into changed/unchanged/skipped/tombstone
// records in memory, then applied to the accumulator sequentially so cache
// and helper collection share one safety model (family commits are eager,
// deletion requires a complete inventory, and an incomplete vendor withholds
// vendor_complete so request_complete — and any coverage-window advance —
// never happens for it).
const (
	claudeParserVersion        = vendors.ParserVersion
	codexParserVersion         = vendors.ParserVersion
	sftpCollectorParserVersion = "sftp-collector.1"
)

func familySkipReason(err error) string {
	switch {
	case errors.Is(err, ErrFileLimit):
		return remotefacts.StaleReasonOversizedFile
	case errors.Is(err, ErrTotalLimit):
		return remotefacts.StaleReasonVendorBudgetExceeded
	case errors.Is(err, ErrSymlink), errors.Is(err, ErrPathDenied):
		return remotefacts.StaleReasonPathDenied
	case errors.Is(err, vendors.ErrInvalidData):
		return remotefacts.StaleReasonInvalidData
	default:
		return remotefacts.StaleReasonReadFailed
	}
}

// vendorFamilyInput is the vendor-neutral shape collectVendorFamilies needs;
// claude- and codex-specific discovery fill it in before the diff/record
// logic (shared, and thus tested once) runs.
type vendorFamilyInput struct {
	SourceID        string
	Source          vendors.ReadSource
	Vendor          string
	ParserVersion   string
	Baseline        map[string]CachedFamilyV2
	Selected        map[string][]vendors.FileFingerprint
	FilesOf         map[string][]string
	AllFamilyIDs    []string
	CandidateFiles  int
	SkippedEntries  int
	Truncated       bool
	Metadata        *vendors.SessionMetadata
	Parse           func(files []string) ([]*vendors.ParsedSession, []vendors.FileFailure, error)
	Fingerprint     func(files []string) ([]vendors.FileFingerprint, error)
	HeaderMappings  map[string][]remotefacts.HeaderMapping
	SessionIDs      map[string][]string
	InitialFailures []vendors.FileFailure
	FamilyIDOf      func(logPath string) string
}

func familyFingerprint(in vendorFamilyInput, id string, fingerprints []vendors.FileFingerprint) string {
	return vendors.AggregateFingerprint(id, fingerprints, in.SessionIDs[id], in.Metadata)
}

type vendorOutcome struct {
	Records  []remoteprotocol.Record
	Complete *remoteprotocol.Record
	Coverage AgentCoverage
	Metadata *vendors.SessionMetadata
	Failures []error
	Err      error
}

func collectVendorFamilies(in vendorFamilyInput) vendorOutcome {
	var records []remoteprotocol.Record
	changed := map[string]string{}
	var toParse []string
	selectedFiles := 0
	for id, fingerprints := range in.Selected {
		selectedFiles += len(fingerprints)
		composite := familyFingerprint(in, id, fingerprints)
		if cached, ok := in.Baseline[id]; ok && cached.Fingerprint == composite &&
			cached.Facts.SchemaVersion == remotefacts.SchemaVersion && cached.Facts.ParserVersion == in.ParserVersion {
			records = append(records, remoteprotocol.Record{
				Type: remoteprotocol.RecordUnchanged, Vendor: in.Vendor, FamilyID: id, Fingerprint: composite,
			})
			continue
		}
		changed[id] = composite
		toParse = append(toParse, in.FilesOf[id]...)
	}

	var parsed []*vendors.ParsedSession
	failures := append([]vendors.FileFailure(nil), in.InitialFailures...)
	if len(toParse) > 0 {
		var parseFailures []vendors.FileFailure
		parsed, parseFailures, _ = in.Parse(toParse)
		failures = append(failures, parseFailures...)
	}
	failedFamilies := map[string]error{}
	for _, failure := range failures {
		failedFamilies[in.FamilyIDOf(failure.Path)] = failure.Err
	}
	byFamily := map[string][]*vendors.ParsedSession{}
	for _, p := range parsed {
		id := in.FamilyIDOf(p.LogPath)
		byFamily[id] = append(byFamily[id], p)
	}
	unstableFamilies := map[string]bool{}
	if len(toParse) > 0 {
		// Recheck each family independently so a disappearing or unstable file
		// cannot cause otherwise valid changed families to be discarded.
		for id, before := range changed {
			after, err := in.Fingerprint(in.FilesOf[id])
			if err != nil || familyFingerprint(in, id, after) != before {
				unstableFamilies[id] = true
			}
		}
	}

	var familyFailures []error
	intentionallyAbsent := map[string]bool{}
	for id, composite := range changed {
		if unstableFamilies[id] {
			records = append(records, remoteprotocol.Record{
				Type: remoteprotocol.RecordSkipped, Vendor: in.Vendor, FamilyID: id, Reason: remotefacts.StaleReasonUnstableFile,
			})
			familyFailures = append(familyFailures, fmt.Errorf("%s family unstable during collection", in.Vendor))
			continue
		}
		if failErr, failed := failedFamilies[id]; failed {
			reason := familySkipReason(failErr)
			records = append(records, remoteprotocol.Record{
				Type: remoteprotocol.RecordSkipped, Vendor: in.Vendor, FamilyID: id, Reason: reason,
			})
			familyFailures = append(familyFailures, fmt.Errorf("%s family skipped: %s", in.Vendor, reason))
			continue
		}
		sessions := byFamily[id]
		if len(sessions) == 0 {
			records = append(records, remoteprotocol.Record{
				Type: remoteprotocol.RecordSkipped, Vendor: in.Vendor, FamilyID: id, Reason: remotefacts.StaleReasonNoData,
			})
			familyFailures = append(familyFailures, fmt.Errorf("%s family skipped: no_data", in.Vendor))
			continue
		}
		var complete []fullsessionv1.Record
		if in.Vendor == vendors.AgentCodex {
			var fullErr error
			complete, fullErr = fullsessionrecord.FromParsedFamily(in.SourceID, in.Vendor, in.Source, sessions, in.Metadata)
			if fullErr != nil {
				records = append(records, remoteprotocol.Record{
					Type: remoteprotocol.RecordSkipped, Vendor: in.Vendor, FamilyID: id, Reason: remotefacts.StaleReasonInvalidData,
				})
				familyFailures = append(familyFailures, fmt.Errorf("%s family skipped: invalid_full_record", in.Vendor))
				continue
			}
			if !containsFullRecord(complete, id) {
				intentionallyAbsent[id] = true
				continue
			}
		} else if !fullsessionrecord.IsServableFamily(id, in.Vendor, in.Source, sessions, in.Metadata) {
			intentionallyAbsent[id] = true
			continue
		}
		family, err := remotefacts.FromParsed(
			in.Vendor, id, in.ParserVersion, remotefacts.StateComplete, "",
			sessions, in.Metadata, in.Selected[id], in.HeaderMappings[id],
		)
		if err != nil {
			records = append(records, remoteprotocol.Record{
				Type: remoteprotocol.RecordSkipped, Vendor: in.Vendor, FamilyID: id, Reason: remotefacts.StaleReasonInvalidData,
			})
			familyFailures = append(familyFailures, fmt.Errorf("%s family skipped: invalid_family_facts", in.Vendor))
			continue
		}
		var fullRecords []remoteprotocol.FullRecord
		if in.Vendor == vendors.AgentCodex {
			for _, completeRecord := range complete {
				fullRecords = append(fullRecords, remoteprotocol.FullRecord{FamilyID: id, Record: completeRecord})
			}
		}
		record := remoteprotocol.Record{
			Type: remoteprotocol.RecordChanged, Vendor: in.Vendor, FamilyID: id,
			Fingerprint: composite, Family: &family, FullRecords: fullRecords,
		}
		if cached, known := in.Baseline[id]; known {
			record.PriorFingerprint = cached.Fingerprint
		}
		records = append(records, record)
	}

	allSet := make(map[string]struct{}, len(in.AllFamilyIDs))
	for _, id := range in.AllFamilyIDs {
		if intentionallyAbsent[id] {
			continue
		}
		allSet[id] = struct{}{}
	}
	inventoryComplete := in.SkippedEntries == 0 && len(allSet) <= remoteprotocol.MaxInventoryFamilies
	if inventoryComplete {
		for id := range in.Baseline {
			if _, present := allSet[id]; !present {
				records = append(records, remoteprotocol.Record{
					Type: remoteprotocol.RecordTombstone, Vendor: in.Vendor, FamilyID: id,
				})
			}
		}
	}
	inventory := []string(nil)
	if inventoryComplete {
		inventory = make([]string, 0, len(allSet))
		for id := range allSet {
			inventory = append(inventory, id)
		}
		sort.Strings(inventory)
	}
	var complete *remoteprotocol.Record
	if in.SkippedEntries == 0 {
		complete = &remoteprotocol.Record{
			Type: remoteprotocol.RecordVendorComplete, Vendor: in.Vendor,
			EnumerationComplete: true, InventoryComplete: inventoryComplete, Inventory: inventory,
		}
	} else {
		familyFailures = append(familyFailures, fmt.Errorf("%s enumeration skipped %d entries", in.Vendor, in.SkippedEntries))
	}
	coverage := AgentCoverage{
		Agent: in.Vendor, CandidateFiles: in.CandidateFiles, SelectedFiles: selectedFiles,
		SkippedEntries: in.SkippedEntries,
		Truncated:      in.Truncated || len(in.AllFamilyIDs) > remoteprotocol.MaxInventoryFamilies,
	}
	return vendorOutcome{Records: records, Complete: complete, Coverage: coverage, Metadata: in.Metadata, Failures: familyFailures}
}

func containsFullRecord(records []fullsessionv1.Record, sessionID string) bool {
	for _, record := range records {
		if record.SessionID == sessionID {
			return true
		}
	}
	return false
}

func collectClaudeVendor(source vendors.ReadSource, sourceID, home string, since int64, now time.Time, baseline map[string]CachedFamilyV2) vendorOutcome {
	metadata := claude.RemoteMetadata(source, home, now)
	selectedFamilies, allFamilyIDs, candidateFiles, skippedEntries, truncated, err := claude.BuildRemoteFamilies(source, home, since, metadata.LiveSessions())
	if err != nil {
		return vendorOutcome{Err: fmt.Errorf("collect Claude remote data: %w", err)}
	}
	selected := map[string][]vendors.FileFingerprint{}
	filesOf := map[string][]string{}
	sessionIDs := map[string][]string{}
	for id, family := range selectedFamilies {
		selected[id] = family.Fingerprints
		filesOf[id] = family.Files
		for _, file := range family.Files {
			sessionIDs[id] = append(sessionIDs[id], claude.SessionIDFromPath(file))
		}
	}
	return collectVendorFamilies(vendorFamilyInput{
		SourceID: sourceID, Source: source,
		Vendor: vendors.AgentClaude, ParserVersion: claudeParserVersion,
		Baseline: baseline, Selected: selected, FilesOf: filesOf, AllFamilyIDs: allFamilyIDs,
		CandidateFiles: candidateFiles, SkippedEntries: skippedEntries, Truncated: truncated, Metadata: metadata,
		SessionIDs: sessionIDs,
		Parse: func(files []string) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
			return claude.ParseRemoteFiles(source, files)
		},
		Fingerprint: func(files []string) ([]vendors.FileFingerprint, error) {
			return vendors.FingerprintSourceFilesFresh(
				source,
				vendors.SourcePathJoin(source, home, ".claude", "projects"),
				files,
			)
		},
		FamilyIDOf: claude.FamilyIDFromPath,
	})
}

func collectCodexVendor(
	source vendors.ReadSource, sourceID, home string, since int64,
	baseline map[string]CachedFamilyV2, cachedHeaders map[string]codex.CachedHeader,
) (vendorOutcome, map[string]codex.CachedHeader) {
	metadata := codex.RemoteMetadata(source, home)
	selectedFamilies, activeFiles, allFamilyIDs, updatedHeaders, headerFailed, candidateFiles, skippedEntries, truncated, err := codex.BuildRemoteFamilies(
		source, home, since, metadata.LiveSessions(), cachedHeaders,
	)
	if err != nil {
		return vendorOutcome{Err: fmt.Errorf("collect Codex remote data: %w", err)}, cachedHeaders
	}
	selected := map[string][]vendors.FileFingerprint{}
	filesOf := map[string][]string{}
	familyIDOf := map[string]string{}
	headerMappings := map[string][]remotefacts.HeaderMapping{}
	sessionIDs := map[string][]string{}
	var initialFailures []vendors.FileFailure
	for id, family := range selectedFamilies {
		selected[id] = family.Fingerprints
		filesOf[id] = family.Files
		for _, fingerprint := range family.Fingerprints {
			if header, ok := updatedHeaders[fingerprint.Key]; ok {
				sessionIDs[id] = append(sessionIDs[id], header.SessionID)
				headerMappings[id] = append(headerMappings[id], remotefacts.HeaderMapping{
					Key: fingerprint.Key, SessionID: header.SessionID, ParentID: header.ParentID,
				})
			}
		}
		for _, file := range family.Files {
			familyIDOf[file] = id
		}
	}
	for file, failure := range headerFailed {
		initialFailures = append(initialFailures, vendors.FileFailure{Path: file, Err: failure})
	}
	outcome := collectVendorFamilies(vendorFamilyInput{
		SourceID: sourceID, Source: source,
		Vendor: vendors.AgentCodex, ParserVersion: codexParserVersion,
		Baseline: baseline, Selected: selected, FilesOf: filesOf, AllFamilyIDs: allFamilyIDs,
		CandidateFiles: candidateFiles, SkippedEntries: skippedEntries, Truncated: truncated, Metadata: metadata,
		Parse: func(files []string) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
			return codex.ParseRemoteFiles(source, home, files, activeFiles)
		},
		Fingerprint: func(files []string) ([]vendors.FileFingerprint, error) {
			return vendors.FingerprintSourceFilesFresh(
				source,
				vendors.SourcePathJoin(source, home, ".codex", "sessions"),
				files,
			)
		},
		HeaderMappings:  headerMappings,
		SessionIDs:      sessionIDs,
		InitialFailures: initialFailures,
		FamilyIDOf: func(logPath string) string {
			if id, ok := familyIDOf[logPath]; ok {
				return id
			}
			return logPath
		},
	})
	return outcome, updatedHeaders
}

func buildLocalRequest(requestID, sourceID string, since, collectedAt int64, baselineID string, known []remoteprotocol.KnownFamily, pageAfter []remoteprotocol.PageCursor) (remoteprotocol.Request, error) {
	request := remoteprotocol.Request{
		RequestID:     requestID,
		Protocol:      remoteprotocol.VersionRange{Min: remoteprotocol.ProtocolVersion, Max: remoteprotocol.ProtocolVersion},
		Schema:        remoteprotocol.VersionRange{Min: remotefacts.SchemaVersion, Max: remotefacts.SchemaVersion},
		ParserVersion: sftpCollectorParserVersion, SourceID: sourceID, SinceMs: since, CollectedAtMs: collectedAt,
		Vendors: []string{vendors.AgentClaude, vendors.AgentCodex}, PageAfter: append([]remoteprotocol.PageCursor(nil), pageAfter...),
		Limits: remoteprotocol.Limits{
			MaxRecordBytes: remoteprotocol.MaxRecordBytes, MaxResponseBytes: remoteprotocol.MaxResponseBytes,
			MaxRecords: remoteprotocol.MaxRecords, MaxInventoryFamilies: remoteprotocol.MaxInventoryFamilies,
		},
	}
	if baselineID == "" {
		request.BaselineMode = remoteprotocol.BaselineNone
		if err := remoteprotocol.ValidateRequest(request); err != nil {
			return remoteprotocol.Request{}, err
		}
		return request, nil
	}
	request.BaselineID = baselineID
	return remoteprotocol.BuildRequest(request, known)
}

func changedRecordFits(record remoteprotocol.Record, requestID string, sequence, maxBytes int) (int, bool, error) {
	record.ProtocolVersion = remoteprotocol.ProtocolVersion
	record.RequestID = requestID
	record.Sequence = sequence
	size := remoteprotocol.EncodedRecordSize(record)
	return size, size <= maxBytes, nil
}

func boundChangedRecord(record remoteprotocol.Record, requestID string, sequence, maxBytes int) (remoteprotocol.Record, bool, int, error) {
	if record.Type != remoteprotocol.RecordChanged {
		return record, false, 0, nil
	}
	size, fits, err := changedRecordFits(record, requestID, sequence, maxBytes)
	if err != nil || fits {
		return record, false, size, err
	}
	return remoteprotocol.Record{
		Type: remoteprotocol.RecordSkipped, Vendor: record.Vendor,
		FamilyID: record.FamilyID, Reason: remotefacts.StaleReasonVendorBudgetExceeded,
	}, true, 0, nil
}

func pageAfterFor(request remoteprotocol.Request, vendor string) string {
	for _, cursor := range request.PageAfter {
		if cursor.Vendor == vendor {
			return cursor.AfterFamilyID
		}
	}
	return ""
}

func sortVendorRecords(records []remoteprotocol.Record, after string) {
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i].FamilyID, records[j].FamilyID
		if after != "" {
			leftPage, rightPage := 1, 1
			if left > after {
				leftPage = 0
			}
			if right > after {
				rightPage = 0
			}
			if leftPage != rightPage {
				return leftPage < rightPage
			}
		}
		return left < right
	})
}

// collectIncremental is the incremental SFTP refresh producer: it diffs each
// vendor against baseline concurrently under an independent byte budget, then
// applies the resulting records to one Accumulator to get the same proposal
// and deletion-authority guarantees as a helper protocol response. Manager
// publishes that proposal only when it contains request_complete.
func collectIncremental(
	source *Source,
	since int64,
	now time.Time,
	baseline CachedSnapshotV2,
) (CachedSnapshotV2, []*session.Session, []error, error) {
	baseline = snapshotOrEmpty(&baseline)
	if baseline.SourceID == "" {
		// Direct collector tests and non-manager callers still need a valid
		// transport identity. Production always supplies the configured ID.
		baseline.SourceID = "r_0000000000000000"
	}
	home := source.Home()
	parseSince := max(0, since-(24*time.Hour).Milliseconds())
	perVendorBudget := source.Limits().MaxTotalBytes / 2
	requestID := fmt.Sprintf("sftp-%d", now.UnixNano())
	request, err := buildLocalRequest(requestID, baseline.SourceID, since, now.UnixMilli(), baseline.BaselineID, knownFamiliesFor(baseline), baseline.PageAfter)
	if err != nil {
		return CachedSnapshotV2{}, nil, nil, fmt.Errorf("build local collection request: %w", err)
	}
	// A baseline-free page does not use cached fingerprints for diffing, but the
	// accumulator retains the last completed page so bounded retries converge.
	effectiveBaseline := baseline
	if request.BaselineMode == remoteprotocol.BaselineNone {
		effectiveBaseline.BaselineID = ""
	}

	claudeBaseline := baselineFamilies(effectiveBaseline, vendors.AgentClaude)
	codexBaseline := baselineFamilies(effectiveBaseline, vendors.AgentCodex)
	if request.BaselineMode == remoteprotocol.BaselineNone {
		claudeBaseline = map[string]CachedFamilyV2{}
		codexBaseline = map[string]CachedFamilyV2{}
	}
	codexHeaders := codexHeaderCacheFrom(baseline.CodexHeaders)

	type claudeResult struct{ outcome vendorOutcome }
	type codexResult struct {
		outcome vendorOutcome
		headers map[string]codex.CachedHeader
	}
	claudeCh := make(chan claudeResult, 1)
	codexCh := make(chan codexResult, 1)
	go func() {
		claudeCh <- claudeResult{collectClaudeVendor(source.ForVendor(perVendorBudget), baseline.SourceID, home, parseSince, now, claudeBaseline)}
	}()
	go func() {
		outcome, headers := collectCodexVendor(source.ForVendor(perVendorBudget), baseline.SourceID, home, parseSince, codexBaseline, codexHeaders)
		codexCh <- codexResult{outcome, headers}
	}()
	claudeOut := <-claudeCh
	codexOut := <-codexCh

	var failures []error
	if claudeOut.outcome.Err != nil {
		failures = append(failures, claudeOut.outcome.Err)
	}
	if codexOut.outcome.Err != nil {
		failures = append(failures, codexOut.outcome.Err)
	}
	failures = append(failures, claudeOut.outcome.Failures...)
	failures = append(failures, codexOut.outcome.Failures...)
	if claudeOut.outcome.Err != nil && codexOut.outcome.Err != nil {
		return CachedSnapshotV2{}, nil, failures, errors.Join(failures...)
	}

	accumulator, err := remoteprotocol.NewAccumulator(request, toGeneration(effectiveBaseline))
	if err != nil {
		return CachedSnapshotV2{}, nil, failures, fmt.Errorf("start local collection accumulator: %w", err)
	}

	sequence := 1
	usedRecords := 0
	usedBytes := 0
	apply := func(record remoteprotocol.Record, preparedSize int) error {
		record.ProtocolVersion = remoteprotocol.ProtocolVersion
		record.RequestID = requestID
		record.Sequence = sequence
		if preparedSize == 0 {
			preparedSize = remoteprotocol.EncodedRecordSize(record)
		}
		if err := accumulator.ApplyEncoded(record, preparedSize); err != nil {
			return err
		}
		sequence++
		usedRecords++
		usedBytes += preparedSize + 1
		return nil
	}
	if err := apply(remoteprotocol.Record{
		Type: remoteprotocol.RecordHandshake, BaselineID: request.BaselineID,
		SchemaVersion: remotefacts.SchemaVersion, ParserVersion: request.ParserVersion,
		Capabilities: []string{remoteprotocol.CapabilityFullSessionRecord},
	}, 0); err != nil {
		return CachedSnapshotV2{}, nil, failures, fmt.Errorf("apply handshake: %w", err)
	}
	completionSize := func(record remoteprotocol.Record) int {
		record.ProtocolVersion = remoteprotocol.ProtocolVersion
		record.RequestID = requestID
		record.Sequence = request.Limits.MaxRecords
		record.Counts = remoteprotocol.Counts{
			CandidateFamilies: remotefacts.MaxCount, SelectedFamilies: remotefacts.MaxCount,
			CandidateFiles: remotefacts.MaxCount, SelectedFiles: remotefacts.MaxCount,
			SkippedFamilies: remotefacts.MaxCount,
		}
		return remoteprotocol.EncodedRecordSize(record) + 1
	}
	reservedRecords, reservedBytes := 0, 0
	allCanComplete := len(failures) == 0
	for _, outcome := range []vendorOutcome{claudeOut.outcome, codexOut.outcome} {
		if outcome.Err != nil || outcome.Complete == nil ||
			(request.BaselineMode == remoteprotocol.BaselineNone && !outcome.Complete.InventoryComplete) {
			allCanComplete = false
			continue
		}
		reservedRecords++
		reservedBytes += completionSize(*outcome.Complete)
	}
	if allCanComplete {
		reservedRecords++
		reservedBytes += completionSize(remoteprotocol.Record{Type: remoteprotocol.RecordRequestComplete})
	}

	var coverage []AgentCoverage
	allVendorsCompleted := true
	for _, vendorName := range []string{vendors.AgentClaude, vendors.AgentCodex} {
		outcome := claudeOut.outcome
		if vendorName == vendors.AgentCodex {
			outcome = codexOut.outcome
		}
		if outcome.Err != nil {
			allVendorsCompleted = false
			coverage = append(coverage, AgentCoverage{
				Agent: vendorName, Error: genericErrorCopy(classifyError(outcome.Err)),
			})
			continue
		}
		vendorLimited := false
		aggregateLimited := false
		aggregateSkipped := 0
		sortVendorRecords(outcome.Records, pageAfterFor(request, vendorName))
		for _, record := range outcome.Records {
			bounded, limited, preparedSize, boundErr := boundChangedRecord(record, requestID, sequence, request.Limits.MaxRecordBytes)
			if boundErr != nil {
				return CachedSnapshotV2{}, nil, failures, fmt.Errorf("size %s changed family: %w", vendorName, boundErr)
			}
			record = bounded
			if limited {
				vendorLimited = true
				failures = append(failures, fmt.Errorf("%s family skipped: %s", vendorName, remotefacts.StaleReasonVendorBudgetExceeded))
			}
			record.ProtocolVersion = remoteprotocol.ProtocolVersion
			record.RequestID = requestID
			record.Sequence = sequence
			if preparedSize == 0 {
				preparedSize = remoteprotocol.EncodedRecordSize(record)
			}
			recordBytes := preparedSize + 1
			if usedRecords+1+reservedRecords > request.Limits.MaxRecords ||
				usedBytes+recordBytes+reservedBytes > request.Limits.MaxResponseBytes {
				aggregateLimited = true
				aggregateSkipped++
				continue
			}
			if err := apply(record, preparedSize); err != nil {
				return CachedSnapshotV2{}, nil, failures, fmt.Errorf("apply %s record: %w", vendorName, err)
			}
		}
		if aggregateLimited {
			outcome.Coverage.Truncated = true
			if outcome.Complete != nil {
				outcome.Complete.Counts.SkippedFamilies += aggregateSkipped
			}
		}
		if vendorLimited {
			allVendorsCompleted = false
			coverage = append(coverage, outcome.Coverage)
			continue
		}
		if outcome.Complete == nil {
			allVendorsCompleted = false
			coverage = append(coverage, outcome.Coverage)
			continue
		}
		// Baseline-free protocol completion requires an authoritative bounded
		// inventory. If it cannot fit, keep the partial facts but do not claim
		// completion or advance coverage.
		if request.BaselineMode == remoteprotocol.BaselineNone && !outcome.Complete.InventoryComplete {
			allVendorsCompleted = false
			coverage = append(coverage, outcome.Coverage)
			continue
		}
		reservedRecords--
		reservedBytes -= completionSize(*outcome.Complete)
		if err := apply(*outcome.Complete, 0); err != nil {
			return CachedSnapshotV2{}, nil, failures, fmt.Errorf("apply %s completion: %w", vendorName, err)
		}
		coverage = append(coverage, outcome.Coverage)
	}
	if allVendorsCompleted && len(failures) == 0 {
		reservedRecords--
		reservedBytes -= completionSize(remoteprotocol.Record{Type: remoteprotocol.RecordRequestComplete})
		if err := apply(remoteprotocol.Record{Type: remoteprotocol.RecordRequestComplete}, 0); err != nil {
			return CachedSnapshotV2{}, nil, failures, fmt.Errorf("apply request completion: %w", err)
		}
	}

	proposal := accumulator.Proposal()
	roundTrip := max(0, time.Since(now).Milliseconds())
	snapshot := fromGeneration(proposal, coverage, now.UnixMilli(), roundTrip, codexHeaderCacheTo(codexOut.headers))
	freshMetadata := map[string]*vendors.SessionMetadata{}
	if claudeOut.outcome.Err == nil {
		freshMetadata[vendors.AgentClaude] = claudeOut.outcome.Metadata
	}
	if codexOut.outcome.Err == nil {
		freshMetadata[vendors.AgentCodex] = codexOut.outcome.Metadata
	}
	sessions := composeFromGeneration(proposal, source, freshMetadata, since)
	return snapshot, sessions, failures, nil
}
