package remotehelper

import (
	"io/fs"
	"time"

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
		result.scan.incompleteWhy = boundedReason("directory scan failed", err)
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

func scanCodex(source *Source, home string, since int64) *vendorScan {
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
		result.scan.incompleteWhy = boundedReason("directory scan failed", err)
		return result
	}
	result.scan.complete = found.SkippedTotal == 0
	if !result.scan.complete {
		result.scan.incompleteWhy = "some directories or files were unreadable"
	}
	result.scan.candidateFiles = len(found.Files)
	headers := codex.HeadersSource(source, found.Files)
	roots := codex.FamilyRoots(headers)
	selected := found.Files
	if since > 0 {
		selected = codex.FilesSinceSource(source, found.Files, metadata.Live, since)
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
		result.record(source, root, familyID, sessionID, file, inWindow[file])
		if header.Err != nil {
			result.scan.family(familyID).skipReason = boundedReason("header unreadable", header.Err)
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
		v.scan.family(familyID).skipReason = boundedReason("file became unreadable", err)
		return
	}
	v.fileFacts[file] = fingerprints[0]
	v.scan.add(familyID, sessionID, fingerprints[0], file, selected)
}

func stringSet(items []string) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item] = true
	}
	return result
}
