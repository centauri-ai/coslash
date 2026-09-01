package remotehelper

import (
	"io/fs"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

// vendorScan is one vendor's enumeration plus the parse entry point for its
// changed families. Grouping reads directory metadata (and, for Codex, header
// rows only); transcript bodies open during parse and never for a family the
// Mac already holds.
type vendorScan struct {
	vendor   string
	source   *Source
	scan     *scan
	metadata *vendors.SessionMetadata
	parse    func(files []string) ([]*vendors.ParsedSession, error)
	// fileFacts is the metadata captured before parsing, which the stability
	// check compares against afterwards.
	fileFacts map[string]vendors.FileFingerprint
}

// statFile re-reads one file's metadata through the same confined source the
// parse used, so a stability check cannot be redirected by a swapped path.
func (v *vendorScan) statFile(file string) (fs.FileInfo, error) {
	return v.source.Stat(file)
}

func scanClaude(
	source *Source,
	home string,
	since int64,
	now time.Time,
	alive func(int) bool,
) *vendorScan {
	metadata := vendors.BestEffortMetadata(
		vendors.AgentClaude,
		func() (*vendors.SessionMetadata, error) {
			return claude.LoadRemoteMetadata(source, home, now, alive)
		},
	)
	result := &vendorScan{
		vendor:    vendors.AgentClaude,
		source:    source,
		scan:      newScan(),
		metadata:  metadata,
		fileFacts: map[string]vendors.FileFingerprint{},
		parse: func(files []string) ([]*vendors.ParsedSession, error) {
			return claude.ParseFamilyFilesSource(source, files)
		},
	}
	root := claude.ProjectsRoot(home)
	found, err := claude.ScanSource(source, root)
	if err != nil {
		result.scan.incompleteWhy = boundedReason(err)
		return result
	}
	result.scan.complete = found.SkippedTotal == 0
	if !result.scan.complete {
		result.scan.incompleteWhy = "some directories or files were unreadable"
	}
	result.scan.candidateFiles = len(found.Files)
	selected := found.Files
	if since > 0 {
		selected = claude.FilesSinceSource(source, found.Files, metadata.Live, since)
	}
	selected, _ = vendors.LimitNewestSourceFileFamilies(
		source, selected, vendors.MaxCandidateFilesPerAgent, claude.FamilyIDFromPath,
	)
	inWindow := stringSet(selected)
	result.scan.selectedFiles = len(selected)
	for _, file := range found.Files {
		sessionID := claude.SessionIDFromPath(file)
		result.record(source, root, claude.FamilyIDFromPath(file), sessionID, file, inWindow[file])
	}
	result.scan.finish(metadata)
	return result
}

func scanCodex(source *Source, home string, request remoteprotocol.Request) *vendorScan {
	metadata := vendors.BestEffortMetadata(
		vendors.AgentCodex,
		func() (*vendors.SessionMetadata, error) { return codex.LoadRemoteMetadata(source, home) },
	)
	result := &vendorScan{
		vendor:    vendors.AgentCodex,
		source:    source,
		scan:      newScan(),
		metadata:  metadata,
		fileFacts: map[string]vendors.FileFingerprint{},
		parse: func(files []string) ([]*vendors.ParsedSession, error) {
			return codex.ParseFamilyFilesSource(source, home, files)
		},
	}
	root := codex.SessionsRoot(home)
	found, err := codex.ScanSource(source, root)
	if err != nil {
		result.scan.incompleteWhy = boundedReason(err)
		return result
	}
	result.scan.complete = found.SkippedTotal == 0
	if !result.scan.complete {
		result.scan.incompleteWhy = "some directories or files were unreadable"
	}
	result.scan.candidateFiles = len(found.Files)
	known := knownCodexHeaders(request)
	headers := make(map[string]codex.FileHeader, len(found.Files))
	fingerprints := make(map[string]vendors.FileFingerprint, len(found.Files))
	readHeaders := make([]string, 0, len(found.Files))
	for _, file := range found.Files {
		items, fingerprintErr := vendors.FingerprintSourceFiles(source, root, []string{file})
		if fingerprintErr == nil && len(items) == 1 {
			fingerprints[file] = items[0]
			if cached, ok := known[items[0].Key]; ok && cached.Size == items[0].Size &&
				cached.ModifiedAtMs == items[0].ModifiedAtMs &&
				cached.SessionID == codex.SessionIDFromRollout(file) {
				headers[file] = codex.FileHeader{SessionID: cached.SessionID, ParentID: cached.ParentID}
				continue
			}
		}
		readHeaders = append(readHeaders, file)
	}
	for file, header := range codex.HeadersSource(source, readHeaders) {
		headers[file] = header
	}
	roots := codex.FamilyRoots(headers)
	selected := found.Files
	if request.SinceMs > 0 {
		selected = codex.FilesSinceSource(source, found.Files, metadata.Live, request.SinceMs)
	}
	selected, _ = vendors.LimitNewestSourceFileFamilies(
		source, selected, vendors.MaxCandidateFilesPerAgent,
		func(file string) string {
			if id := roots[file]; id != "" {
				return id
			}
			return file
		},
	)
	inWindow := stringSet(selected)
	result.scan.selectedFiles = len(selected)
	for _, file := range found.Files {
		familyID := roots[file]
		if familyID == "" {
			// An unattributable rollout could belong to a family that would
			// otherwise look absent, so enumeration is no longer authoritative.
			result.scan.complete = false
			result.scan.incompleteWhy = "a rollout could not be attributed to a family"
			continue
		}
		header := headers[file]
		sessionID := header.SessionID
		if sessionID == "" {
			sessionID = codex.SessionIDFromRollout(file)
		}
		fingerprint, ok := fingerprints[file]
		if !ok {
			result.record(source, root, familyID, sessionID, file, inWindow[file])
		} else {
			result.recordFingerprint(familyID, sessionID, file, fingerprint, inWindow[file])
		}
		if ok && header.Err == nil && header.SessionID != "" {
			item := result.scan.family(familyID)
			item.headerMappings = append(item.headerMappings, remotefacts.HeaderMapping{
				Key: fingerprint.Key, SessionID: header.SessionID, ParentID: header.ParentID,
			})
		}
		if header.Err != nil {
			result.scan.family(familyID).skipReason = boundedReason(header.Err)
		}
	}
	result.scan.finish(metadata)
	return result
}

// record fingerprints one file and files it under its family. A file that
// vanished or became unreadable between listing and stat marks its family
// skipped for this generation; it is never treated as a deletion.
func (v *vendorScan) record(
	source *Source,
	root, familyID, sessionID, file string,
	selected bool,
) {
	fingerprints, err := vendors.FingerprintSourceFiles(source, root, []string{file})
	if err != nil || len(fingerprints) != 1 {
		v.scan.family(familyID).skipReason = boundedReason(err)
		return
	}
	v.recordFingerprint(familyID, sessionID, file, fingerprints[0], selected)
}

func (v *vendorScan) recordFingerprint(
	familyID, sessionID, file string,
	fingerprint vendors.FileFingerprint,
	selected bool,
) {
	v.fileFacts[file] = fingerprint
	v.scan.add(familyID, sessionID, fingerprint, file, selected)
}

func knownCodexHeaders(request remoteprotocol.Request) map[string]remoteprotocol.KnownHeader {
	result := map[string]remoteprotocol.KnownHeader{}
	if request.BaselineMode != remoteprotocol.BaselineKnown {
		return result
	}
	for _, family := range request.Known {
		if family.Vendor != vendors.AgentCodex {
			continue
		}
		for _, header := range family.Headers {
			result[header.Key] = header
		}
	}
	return result
}

func stringSet(items []string) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item] = true
	}
	return result
}
