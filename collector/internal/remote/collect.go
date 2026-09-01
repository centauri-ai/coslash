package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

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
	claudeParserVersion        = "claude-sftp/1"
	codexParserVersion         = "codex-sftp/1"
	sftpCollectorParserVersion = "sftp-collector.1"
)

func compositeFingerprint(fingerprints []vendors.FileFingerprint) string {
	sorted := append([]vendors.FileFingerprint(nil), fingerprints...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	h := sha256.New()
	for _, fp := range sorted {
		fmt.Fprintf(h, "%s:%d:%d\n", fp.Key, fp.Size, fp.ModifiedAtMs)
	}
	return hex.EncodeToString(h.Sum(nil))
}

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
	Vendor          string
	ParserVersion   string
	Baseline        map[string]CachedFamilyV2
	Selected        map[string][]vendors.FileFingerprint
	FilesOf         map[string][]string
	AllFamilyIDs    []string
	CandidateFiles  int
	Truncated       bool
	Metadata        *vendors.SessionMetadata
	Parse           func(files []string) ([]*vendors.ParsedSession, []vendors.FileFailure, error)
	Fingerprint     func(files []string) ([]vendors.FileFingerprint, error)
	InitialFailures []vendors.FileFailure
	FamilyIDOf      func(logPath string) string
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
		composite := compositeFingerprint(fingerprints)
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
			if err != nil || compositeFingerprint(after) != before {
				unstableFamilies[id] = true
			}
		}
	}

	var familyFailures []error
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
		family, err := remotefacts.FromParsed(
			in.Vendor, id, in.ParserVersion, remotefacts.StateComplete, "",
			sessions, in.Metadata, in.Selected[id],
		)
		if err != nil {
			records = append(records, remoteprotocol.Record{
				Type: remoteprotocol.RecordSkipped, Vendor: in.Vendor, FamilyID: id, Reason: remotefacts.StaleReasonInvalidData,
			})
			familyFailures = append(familyFailures, fmt.Errorf("%s family skipped: invalid_family_facts", in.Vendor))
			continue
		}
		record := remoteprotocol.Record{
			Type: remoteprotocol.RecordChanged, Vendor: in.Vendor, FamilyID: id,
			Fingerprint: composite, Family: &family,
		}
		if cached, known := in.Baseline[id]; known {
			record.PriorFingerprint = cached.Fingerprint
		}
		records = append(records, record)
	}

	allSet := make(map[string]struct{}, len(in.AllFamilyIDs))
	for _, id := range in.AllFamilyIDs {
		allSet[id] = struct{}{}
	}
	inventoryComplete := len(in.AllFamilyIDs) <= remoteprotocol.MaxInventoryFamilies
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
		inventory = append([]string(nil), in.AllFamilyIDs...)
		sort.Strings(inventory)
	}
	complete := &remoteprotocol.Record{
		Type: remoteprotocol.RecordVendorComplete, Vendor: in.Vendor,
		EnumerationComplete: true, InventoryComplete: inventoryComplete, Inventory: inventory,
	}
	coverage := AgentCoverage{
		Agent: in.Vendor, CandidateFiles: in.CandidateFiles, SelectedFiles: selectedFiles,
		Truncated: in.Truncated || !inventoryComplete,
	}
	return vendorOutcome{Records: records, Complete: complete, Coverage: coverage, Metadata: in.Metadata, Failures: familyFailures}
}

func collectClaudeVendor(source vendors.ReadSource, home string, since int64, now time.Time, baseline map[string]CachedFamilyV2) vendorOutcome {
	metadata := claude.RemoteMetadata(source, home, now)
	selectedFamilies, allFamilyIDs, candidateFiles, truncated, err := claude.BuildRemoteFamilies(source, home, since, metadata.Live)
	if err != nil {
		return vendorOutcome{Err: fmt.Errorf("collect Claude remote data: %w", err)}
	}
	selected := map[string][]vendors.FileFingerprint{}
	filesOf := map[string][]string{}
	for id, family := range selectedFamilies {
		selected[id] = family.Fingerprints
		filesOf[id] = family.Files
	}
	return collectVendorFamilies(vendorFamilyInput{
		Vendor: vendors.AgentClaude, ParserVersion: claudeParserVersion,
		Baseline: baseline, Selected: selected, FilesOf: filesOf, AllFamilyIDs: allFamilyIDs,
		CandidateFiles: candidateFiles, Truncated: truncated, Metadata: metadata,
		Parse: func(files []string) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
			return claude.ParseRemoteFiles(source, files)
		},
		Fingerprint: func(files []string) ([]vendors.FileFingerprint, error) {
			return vendors.FingerprintSourceFilesFresh(source, claude.ProjectsRoot(home), files)
		},
		FamilyIDOf: claude.FamilyIDFromPath,
	})
}

func collectCodexVendor(
	source vendors.ReadSource, home string, since int64,
	baseline map[string]CachedFamilyV2, cachedHeaders map[string]codex.CachedHeader,
) (vendorOutcome, map[string]codex.CachedHeader) {
	metadata := codex.RemoteMetadata(source, home)
	selectedFamilies, allFamilyIDs, updatedHeaders, headerFailed, candidateFiles, truncated, err := codex.BuildRemoteFamilies(
		source, home, since, metadata.Live, cachedHeaders,
	)
	if err != nil {
		return vendorOutcome{Err: fmt.Errorf("collect Codex remote data: %w", err)}, cachedHeaders
	}
	selected := map[string][]vendors.FileFingerprint{}
	filesOf := map[string][]string{}
	familyIDOf := map[string]string{}
	var initialFailures []vendors.FileFailure
	for id, family := range selectedFamilies {
		selected[id] = family.Fingerprints
		filesOf[id] = family.Files
		for _, file := range family.Files {
			familyIDOf[file] = id
		}
	}
	for file, failure := range headerFailed {
		initialFailures = append(initialFailures, vendors.FileFailure{Path: file, Err: failure})
	}
	outcome := collectVendorFamilies(vendorFamilyInput{
		Vendor: vendors.AgentCodex, ParserVersion: codexParserVersion,
		Baseline: baseline, Selected: selected, FilesOf: filesOf, AllFamilyIDs: allFamilyIDs,
		CandidateFiles: candidateFiles, Truncated: truncated, Metadata: metadata,
		Parse: func(files []string) ([]*vendors.ParsedSession, []vendors.FileFailure, error) {
			return codex.ParseRemoteFiles(source, home, files)
		},
		Fingerprint: func(files []string) ([]vendors.FileFingerprint, error) {
			return vendors.FingerprintSourceFilesFresh(source, codex.SessionsRoot(home), files)
		},
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

func buildLocalRequest(requestID string, since, collectedAt int64, baselineID string, known []remoteprotocol.KnownFamily) (remoteprotocol.Request, error) {
	request := remoteprotocol.Request{
		RequestID:     requestID,
		Protocol:      remoteprotocol.VersionRange{Min: remoteprotocol.ProtocolVersion, Max: remoteprotocol.ProtocolVersion},
		Schema:        remoteprotocol.VersionRange{Min: remotefacts.SchemaVersion, Max: remotefacts.SchemaVersion},
		ParserVersion: sftpCollectorParserVersion, SinceMs: since, CollectedAtMs: collectedAt,
		Vendors: []string{vendors.AgentClaude, vendors.AgentCodex},
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

// collectIncremental is the incremental SFTP refresh producer: it diffs each
// vendor against baseline concurrently under an independent byte budget, then
// applies the resulting records to one Accumulator to get the same partial-
// merge and deletion-authority guarantees a helper's protocol response would
// get.
func collectIncremental(
	source *Source,
	since int64,
	now time.Time,
	baseline CachedSnapshotV2,
) (CachedSnapshotV2, []*session.Session, []error, error) {
	home := source.Home()
	parseSince := max(0, since-(24*time.Hour).Milliseconds())
	perVendorBudget := source.Limits().MaxTotalBytes / 2
	requestID := fmt.Sprintf("sftp-%d", now.UnixNano())
	request, err := buildLocalRequest(requestID, since, now.UnixMilli(), baseline.BaselineID, knownFamiliesFor(baseline))
	if err != nil {
		return CachedSnapshotV2{}, nil, nil, fmt.Errorf("build local collection request: %w", err)
	}
	// When the bounded known-family set cannot fit, BuildRequest deliberately
	// falls back to baseline_mode=none. Do not continue diffing against or
	// copying the old facts in that mode: it requires a bounded full recollect.
	effectiveBaseline := baseline
	if request.BaselineMode == remoteprotocol.BaselineNone {
		effectiveBaseline = CachedSnapshotV2{Version: cacheV2Version, CodexHeaders: baseline.CodexHeaders}
	}

	claudeBaseline := baselineFamilies(effectiveBaseline, vendors.AgentClaude)
	codexBaseline := baselineFamilies(effectiveBaseline, vendors.AgentCodex)
	codexHeaders := codexHeaderCacheFrom(baseline.CodexHeaders)

	type claudeResult struct{ outcome vendorOutcome }
	type codexResult struct {
		outcome vendorOutcome
		headers map[string]codex.CachedHeader
	}
	claudeCh := make(chan claudeResult, 1)
	codexCh := make(chan codexResult, 1)
	go func() {
		claudeCh <- claudeResult{collectClaudeVendor(source.ForVendor(perVendorBudget), home, parseSince, now, claudeBaseline)}
	}()
	go func() {
		outcome, headers := collectCodexVendor(source.ForVendor(perVendorBudget), home, parseSince, codexBaseline, codexHeaders)
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
	apply := func(record remoteprotocol.Record) error {
		record.ProtocolVersion = remoteprotocol.ProtocolVersion
		record.RequestID = requestID
		record.Sequence = sequence
		sequence++
		return accumulator.Apply(record)
	}
	if err := apply(remoteprotocol.Record{
		Type: remoteprotocol.RecordHandshake, BaselineID: request.BaselineID,
		SchemaVersion: remotefacts.SchemaVersion, ParserVersion: request.ParserVersion,
	}); err != nil {
		return CachedSnapshotV2{}, nil, failures, fmt.Errorf("apply handshake: %w", err)
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
		for _, record := range outcome.Records {
			if err := apply(record); err != nil {
				return CachedSnapshotV2{}, nil, failures, fmt.Errorf("apply %s record: %w", vendorName, err)
			}
		}
		// Baseline-free protocol completion requires an authoritative bounded
		// inventory. If it cannot fit, keep the partial facts but do not claim
		// completion or advance coverage.
		if request.BaselineMode == remoteprotocol.BaselineNone && !outcome.Complete.InventoryComplete {
			allVendorsCompleted = false
			coverage = append(coverage, outcome.Coverage)
			continue
		}
		if err := apply(*outcome.Complete); err != nil {
			return CachedSnapshotV2{}, nil, failures, fmt.Errorf("apply %s completion: %w", vendorName, err)
		}
		coverage = append(coverage, outcome.Coverage)
	}
	if allVendorsCompleted && len(failures) == 0 {
		if err := apply(remoteprotocol.Record{Type: remoteprotocol.RecordRequestComplete}); err != nil {
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
