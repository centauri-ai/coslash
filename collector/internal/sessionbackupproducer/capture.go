package sessionbackupproducer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type artifactWriter struct {
	root       string
	ctx        context.Context
	evidence   map[string]sessionbackupv1.ArtifactEvidence
	artifacts  []sessionbackupv1.Artifact
	totalBytes int64
}

func (manager *Manager) Prepare(ctx context.Context, selection Selection) (*Prepared, error) {
	if selection.Agent != vendors.AgentCodex &&
		!((selection.Agent == vendors.AgentClaude || selection.Agent == vendors.AgentOpenCode || selection.Agent == vendors.AgentCursor) &&
			selection.SourceKind == sessionbackupv1.SourceLocal) {
		return problem(selection, sessionbackupv1.ProblemUnsupported, "", false)
	}
	if selection.SourceKind != sessionbackupv1.SourceLocal && selection.SourceKind != sessionbackupv1.SourceSSH {
		return problem(selection, sessionbackupv1.ProblemInvalid, "", false)
	}
	if selection.SourceID == "" || selection.SessionID == "" {
		return problem(selection, sessionbackupv1.ProblemInvalid, "", false)
	}
	if err := ctx.Err(); err != nil {
		return problem(selection, sessionbackupv1.ProblemUnavailable, "", true)
	}
	if err := os.MkdirAll(manager.root, 0o700); err != nil {
		return problem(selection, sessionbackupv1.ProblemUnavailable, "", true)
	}
	staging, err := os.MkdirTemp(manager.root, ".preparing-*")
	if err != nil {
		return problem(selection, sessionbackupv1.ProblemUnavailable, "", true)
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		_ = os.RemoveAll(staging)
		return problem(selection, sessionbackupv1.ProblemUnavailable, "", true)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staging)
		}
	}()

	if selection.Agent == vendors.AgentOpenCode {
		prepared, err := manager.captureOpenCode(ctx, staging, selection)
		if err != nil {
			var captureErr *captureError
			if errors.As(err, &captureErr) {
				return captureProblem(selection, captureErr)
			}
			return problem(selection, sessionbackupv1.ProblemInvalid, "", false)
		}
		return manager.publish(ctx, staging, selection, prepared, &keep)
	}
	handle, err := manager.openSource(ctx, selection)
	if err != nil || handle.Source == nil || handle.Home == "" {
		return problem(selection, sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
	}
	if handle.Close != nil {
		defer handle.Close()
	}
	var prepared *Prepared
	if selection.Agent == vendors.AgentCursor {
		prepared, err = manager.captureCursor(ctx, staging, selection, handle)
	} else {
		prepared, err = manager.capture(ctx, staging, selection, handle)
	}
	if err != nil {
		var captureErr *captureError
		if errors.As(err, &captureErr) {
			return captureProblem(selection, captureErr)
		}
		return problem(selection, sessionbackupv1.ProblemInvalid, "", false)
	}

	return manager.publish(ctx, staging, selection, prepared, &keep)
}

func (manager *Manager) publish(ctx context.Context, staging string, selection Selection, prepared *Prepared, keep *bool) (*Prepared, error) {
	destination := filepath.Join(manager.root, prepared.BundleID)
	if _, err := os.Stat(destination); err == nil {
		existing, openErr := manager.Open(prepared.BundleID)
		if openErr != nil {
			return problem(selection, sessionbackupv1.ProblemInvalid, "", false)
		}
		return existing, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return problem(selection, sessionbackupv1.ProblemUnavailable, "", true)
	}
	if err := os.Rename(staging, destination); err != nil {
		return problem(selection, sessionbackupv1.ProblemUnavailable, "", true)
	}
	if manager.afterRename != nil {
		manager.afterRename()
	}
	if ctx.Err() != nil {
		_ = os.RemoveAll(destination)
		return problem(selection, sessionbackupv1.ProblemUnavailable, "", true)
	}
	*keep = true
	prepared.ownsBundle = true
	return prepared, nil
}

type captureError struct {
	code      string
	kind      string
	retryable bool
	tooLarge  bool
}

var errArtifactTooLarge = fmt.Errorf("%w: size limit exceeded", sessionbackupv1.ErrInvalid)

func (err *captureError) Error() string { return err.code }

func captureFailure(code, kind string, retryable bool) error {
	return &captureError{code: code, kind: kind, retryable: retryable}
}

func captureTooLarge(kind string) error {
	return &captureError{code: sessionbackupv1.ProblemInvalid, kind: kind, tooLarge: true}
}

func sourceReadFailure(err error, kind string) error {
	if errors.Is(err, errArtifactTooLarge) {
		return captureTooLarge(kind)
	}
	if errors.Is(err, vendors.ErrInvalidData) {
		return captureFailure(sessionbackupv1.ProblemUnattributable, kind, false)
	}
	return captureFailure(sessionbackupv1.ProblemUnreadable, kind, true)
}

func processedCaptureFailure(err error, kind string) error {
	if errors.Is(err, errArtifactTooLarge) {
		return captureTooLarge(kind)
	}
	return captureFailure(sessionbackupv1.ProblemInvalid, kind, false)
}

func (manager *Manager) capture(ctx context.Context, staging string, selection Selection, handle SourceHandle) (*Prepared, error) {
	if selection.Agent == vendors.AgentClaude {
		return manager.captureClaude(ctx, staging, selection, handle)
	}
	activeScan, err := codex.ScanSourceContext(ctx, handle.Source, codex.SessionsRoot(handle.Home))
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
	}
	archivedScan, err := codex.ScanSourceContext(ctx, handle.Source, codex.ArchivedDir(handle.Home))
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
	}
	if activeScan.SkippedTotal > 0 || archivedScan.SkippedTotal > 0 {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
	}
	allFiles := append(append([]string(nil), activeScan.Files...), archivedScan.Files...)
	for _, file := range allFiles {
		if err := ctx.Err(); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
		}
		info, statErr := handle.Source.Stat(file)
		if statErr != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
		}
		if info.Size() > sessionbackupv1.MaxArtifactBytes {
			return nil, captureTooLarge(sessionbackupv1.KindRawTranscript)
		}
		if info.Size() < 0 {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawTranscript, false)
		}
	}
	headers, err := codex.HeadersSourceContext(ctx, handle.Source, allFiles)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
	}
	roots := codex.FamilyRoots(headers)
	var familyFiles []string
	for _, file := range allFiles {
		if roots[file] == selection.SessionID {
			familyFiles = append(familyFiles, file)
		}
	}
	if len(familyFiles) == 0 {
		return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
	}
	sort.Slice(familyFiles, func(i, j int) bool {
		left, right := headers[familyFiles[i]].SessionID, headers[familyFiles[j]].SessionID
		if left == right {
			return familyFiles[i] < familyFiles[j]
		}
		return left < right
	})
	before, err := vendors.FingerprintSourceFilesContext(ctx, handle.Source, handle.Home, familyFiles)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
	}
	if !withinKnownBounds(before) {
		if exceedsKnownBounds(before) {
			return nil, captureTooLarge(sessionbackupv1.KindRawTranscript)
		}
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawTranscript, false)
	}

	memberIDs := map[string]bool{}
	hiddenParents := map[string]string{}
	for _, file := range familyFiles {
		if header := headers[file]; header.Hidden {
			hiddenParents[header.SessionID] = header.ParentID
		} else {
			memberIDs[header.SessionID] = true
		}
	}
	if len(memberIDs) > sessionbackupv1.MaxMembers {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawTranscript, false)
	}
	// A hidden guardian rollout has no parsed record of its own, so its exact
	// bytes belong to the nearest represented ancestor whose work it reviewed.
	owners := make(map[string]string, len(familyFiles))
	hiddenOwners := make(map[string]string, len(hiddenParents))
	for _, file := range familyFiles {
		header := headers[file]
		owner := header.SessionID
		if header.Hidden {
			owner = header.ParentID
		}
		for steps := 0; !memberIDs[owner]; steps++ {
			parent, hidden := hiddenParents[owner]
			if !hidden || parent == "" || steps > len(hiddenParents) {
				return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
			}
			owner = parent
		}
		if header.Hidden {
			if owner == header.SessionID {
				return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
			}
			if previousOwner, exists := hiddenOwners[header.SessionID]; exists && previousOwner != owner {
				return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
			}
			hiddenOwners[header.SessionID] = owner
		}
		owners[file] = owner
	}
	indexIDs := maps.Clone(memberIDs)
	for id := range hiddenParents {
		indexIDs[id] = true
	}
	indexPath := codex.SessionIndexPath(handle.Home)
	var indexBefore []vendors.FileFingerprint
	if _, statErr := handle.Source.Stat(indexPath); statErr == nil {
		indexBefore, err = vendors.FingerprintSourceFilesContext(ctx, handle.Source, handle.Home, []string{indexPath})
		if err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawSidecar, true)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawSidecar, true)
	}
	indexRows, indexPresent, err := codex.ReadSessionIndexRowsContext(ctx, handle.Source, handle.Home, indexIDs)
	if err != nil {
		return nil, sourceReadFailure(err, sessionbackupv1.KindRawSidecar)
	}
	if indexPresent != (len(indexBefore) == 1) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawSidecar, true)
	}
	metadata, err := metadataFromRows(ctx, indexRows)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawSidecar, true)
	}
	synthesisRecords := map[string]sessionbackupv1.SynthesisRecord{}
	if len(familyFiles)+len(indexRows)+2*len(memberIDs) > sessionbackupv1.MaxArtifacts {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
	}
	writes := &artifactWriter{root: staging, ctx: ctx, evidence: map[string]sessionbackupv1.ArtifactEvidence{}}
	rawEvidenceByMember := map[string][]string{}
	frozenFiles := map[string]string{}
	rolloutIndex := map[string]int{}
	for _, file := range familyFiles {
		if err := ctx.Err(); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
		}
		memberID := owners[file]
		index := rolloutIndex[memberID]
		rolloutIndex[memberID]++
		name := fmt.Sprintf("members/%s/raw/rollout-%06d.jsonl", memberID, index)
		artifact := sessionbackupv1.Artifact{
			LogicalName: name, MemberID: memberID, Source: sessionbackupv1.ArtifactSourceCodex,
			Kind: sessionbackupv1.KindRawTranscript, SourceKey: fmt.Sprintf("rollout-%06d", index),
			MediaType: "application/x-ndjson", Encoding: sessionbackupv1.EncodingIdentity,
		}
		if err := writes.stream(ctx, artifact, func() (io.ReadCloser, error) { return handle.Source.Open(file) }); err != nil {
			if errors.Is(err, errArtifactTooLarge) {
				return nil, captureTooLarge(sessionbackupv1.KindRawTranscript)
			}
			if errors.Is(err, sessionbackupv1.ErrInvalid) {
				return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawTranscript, false)
			}
			return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
		}
		frozenFiles[file] = filepath.Join(staging, filepath.FromSlash(name))
		rawEvidenceByMember[memberID] = append(rawEvidenceByMember[memberID], writes.evidence[name].SHA256)
	}
	for sessionID, rows := range indexRows {
		memberID, sourceKey := sessionID, "session_index"
		name := fmt.Sprintf("members/%s/raw/session-index-row.jsonl", memberID)
		if owner, hidden := hiddenOwners[sessionID]; hidden {
			memberID = owner
			sourceKey += "-" + sessionID
			name = fmt.Sprintf("members/%s/raw/session-index-%s.jsonl", memberID, sessionID)
		}
		artifact := sessionbackupv1.Artifact{
			LogicalName: name, MemberID: memberID, Source: sessionbackupv1.ArtifactSourceCodex,
			Kind: sessionbackupv1.KindRawSidecar, SourceKey: sourceKey, MediaType: "application/x-ndjson",
			Encoding: sessionbackupv1.EncodingIdentity,
		}
		if err := writes.stream(ctx, artifact, func() (io.ReadCloser, error) {
			readers := make([]io.Reader, 0, len(rows))
			for _, row := range rows {
				readers = append(readers, bytes.NewReader(row))
			}
			return io.NopCloser(io.MultiReader(readers...)), nil
		}); err != nil {
			return nil, err
		}
		rawEvidenceByMember[memberID] = append(rawEvidenceByMember[memberID], writes.evidence[name].SHA256)
	}
	if manager.afterRawCopy != nil {
		manager.afterRawCopy()
	}
	after, err := vendors.FingerprintSourceFilesFreshContext(ctx, handle.Source, handle.Home, familyFiles)
	if err != nil || !reflect.DeepEqual(before, after) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	}
	if indexPresent {
		indexAfter, statErr := vendors.FingerprintSourceFilesFreshContext(ctx, handle.Source, handle.Home, []string{indexPath})
		if statErr != nil || !reflect.DeepEqual(indexBefore, indexAfter) {
			return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawSidecar, true)
		}
	}
	// Parse only from the exact frozen rollout bytes, binding every processed
	// artifact to the bytes that will actually be uploaded without consuming a
	// second full read from the live SSH byte budget.
	frozenSource := snapshotSource{ReadSource: handle.Source, files: frozenFiles}
	activeFamilyFiles := make([]string, 0, len(familyFiles))
	activeSet := map[string]bool{}
	for _, file := range activeScan.Files {
		activeSet[file] = true
	}
	for _, file := range familyFiles {
		if activeSet[file] {
			activeFamilyFiles = append(activeFamilyFiles, file)
		}
	}
	parsed, err := codex.ParseFamilyFilesSourceContext(ctx, frozenSource, handle.Home, familyFiles, activeFamilyFiles)
	if err != nil || len(parsed) != len(memberIDs) {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	parsedFiles := make(map[string]bool, len(parsed))
	for _, item := range parsed {
		if !memberIDs[item.Session.ID] {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
		}
		parsedFiles[item.LogPath] = true
	}
	for _, file := range familyFiles {
		if parsedFiles[file] == headers[file].Hidden {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
		}
	}
	records, err := fullsessionrecord.FromParsedFamilyContext(ctx, selection.SourceID, vendors.AgentCodex, frozenSource, parsed, metadata)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	if selection.SourceKind == sessionbackupv1.SourceLocal && manager.synthesis != nil {
		revisionByID := map[string]int64{}
		for _, record := range records {
			revisionByID[record.SessionID] = record.Session.LastActivityAtMs
		}
		for _, item := range parsed {
			if err := ctx.Err(); err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindSynthesis, true)
			}
			persisted, loadErr := manager.synthesis.LoadRecord(vendors.AgentCodex, item.Session.ID)
			if err := ctx.Err(); err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindSynthesis, true)
			}
			if errors.Is(loadErr, fs.ErrNotExist) {
				continue
			}
			if loadErr != nil {
				return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSynthesis, false)
			}
			if persisted.Revision != revisionByID[item.Session.ID] {
				continue
			}
			synthesisRecords[item.Session.ID] = sessionbackupv1.SynthesisRecord{
				Agent: vendors.AgentCodex, SessionID: item.Session.ID, Revision: persisted.Revision,
				Model: persisted.Model, GeneratedAt: persisted.GeneratedAt,
				Synthesis: fullsessionv1.SessionSynthesis{
					Goals: append([]string(nil), persisted.Synthesis.Goals...), Outcome: persisted.Synthesis.Outcome,
					KeyDecisions: append([]string(nil), persisted.Synthesis.KeyDecisions...), NextStep: persisted.Synthesis.NextStep,
				},
			}
		}
	}
	expectedArtifacts := len(writes.artifacts) + 2*len(records) + len(synthesisRecords)
	for _, record := range records {
		for _, edit := range record.Session.FileEdits {
			expectedArtifacts += len(edit.Changes)
		}
	}
	if expectedArtifacts > sessionbackupv1.MaxArtifacts {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
	}
	recordByID := make(map[string]fullsessionv1.Record, len(records))
	for _, record := range records {
		if _, duplicate := recordByID[record.SessionID]; duplicate {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
		}
		recordByID[record.SessionID] = record
	}
	rootRecord, ok := recordByID[selection.SessionID]
	if !ok || len(recordByID) != len(memberIDs) {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
	}
	repository, repositoryLocalOnly := repositoryIdentity(ctx, selection.SourceKind, rootRecord.Session.WorkingDirectory, handle.Enrichment[selection.SessionID])
	if repository == "" {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSessionEnrichment, false)
	}

	members := make([]sessionbackupv1.Member, 0, len(recordByID))
	memberRevisions := map[string]string{}
	for memberID, record := range recordByID {
		memberRevision := hashStrings(rawEvidenceByMember[memberID])
		memberRevisions[memberID] = memberRevision
		synthesisRevision := int64(0)
		if persisted, exists := synthesisRecords[memberID]; exists {
			synthesisRevision = persisted.Revision
		}
		members = append(members, sessionbackupv1.Member{
			MemberID: memberID, ParentMemberID: record.ParentSessionID,
			SourceRevision: memberRevision, SynthesisRevisionMs: synthesisRevision,
		})
		if err := writes.addProcessed(ctx, record, selection.SourceKind, repository, repositoryLocalOnly, handle.Enrichment[memberID], synthesisRecords[memberID]); err != nil {
			return nil, processedCaptureFailure(err, "")
		}
	}
	sourceRevisionInput := make([]string, 0, len(memberRevisions))
	for memberID, revision := range memberRevisions {
		sourceRevisionInput = append(sourceRevisionInput, memberID+":"+revision)
	}
	sort.Strings(sourceRevisionInput)
	manifest := sessionbackupv1.Manifest{
		RequiredVersions: []string{sessionbackupv1.ParsedRecordVersion, sessionbackupv1.SchemaVersion},
		Source: sessionbackupv1.SourceIdentity{
			Kind: selection.SourceKind, SourceID: selection.SourceID, Agent: vendors.AgentCodex,
			SourceRevision: hashStrings(sourceRevisionInput),
		},
		Repository: sessionbackupv1.RepositoryIdentity{Canonical: repository, VCS: "git"},
		Producer:   sessionbackupv1.ProducerIdentity{Name: "coslash", Version: manager.collectorVersion, ParserVersion: manager.parserVersion},
		Family:     sessionbackupv1.FamilyIdentity{FamilyID: selection.SessionID, RootMemberID: selection.SessionID},
		Members:    members, Artifacts: writes.artifacts,
	}
	frozen, err := sessionbackupv1.FreezeEvidence(manifest, writes.evidence)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
	}
	manifestBytes, err := sessionbackupv1.Marshal(frozen)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
	}
	if err := os.WriteFile(filepath.Join(staging, sessionbackupv1.ManifestFileName), manifestBytes, 0o600); err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnavailable, "", true)
	}
	return &Prepared{BundleID: frozen.CompleteBackupSHA256, Selection: selection, Manifest: frozen, Coverage: coverage(frozen)}, nil
}

func metadataFromRows(ctx context.Context, rows map[string][][]byte) (*vendors.SessionMetadata, error) {
	metadata := vendors.EmptySessionMetadata()
	for id, memberRows := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, row := range memberRows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var entry struct {
				ThreadName *string `json:"thread_name"`
			}
			if json.Unmarshal(row, &entry) == nil && entry.ThreadName != nil {
				// Match the local index reader's last-name behavior while retaining
				// every duplicate row in the raw sidecar artifact.
				metadata.Session(id).Name = *entry.ThreadName
			}
		}
	}
	return metadata, nil
}

// manifestRepository names the family's Git repository, or records that the
// session has no working folder.
func manifestRepository(repository string) sessionbackupv1.RepositoryIdentity {
	if repository == "" {
		return sessionbackupv1.RepositoryIdentity{VCS: sessionbackupv1.RepositoryVCSNone}
	}
	return sessionbackupv1.RepositoryIdentity{Canonical: repository, VCS: sessionbackupv1.RepositoryVCSGit}
}

func repositoryIdentity(ctx context.Context, sourceKind, workingDirectory string, frozen remote.BackupSessionEnrichment) (string, bool) {
	if sourceKind == sessionbackupv1.SourceLocal {
		return session.CanonicalRepositoryNameContext(ctx, workingDirectory)
	}
	if frozen.Repository != nil && *frozen.Repository != "" {
		return *frozen.Repository, frozen.RepositoryLocalOnly
	}
	name := path.Base(path.Clean(workingDirectory))
	if name == "." || name == "/" {
		return "", true
	}
	return name, true
}

func (writes *artifactWriter) addProcessed(ctx context.Context, record fullsessionv1.Record, sourceKind, repository string, repositoryLocalOnly bool, frozen remote.BackupSessionEnrichment, persisted sessionbackupv1.SynthesisRecord) error {
	prefix := fmt.Sprintf("members/%s/processed", record.SessionID)
	recordBytes, err := fullsessionv1.Marshal(record)
	if err != nil {
		return err
	}
	if err := writes.bytes(sessionbackupv1.Artifact{
		LogicalName: prefix + "/full-session-record.json", MemberID: record.SessionID,
		Source: sessionbackupv1.ArtifactSourceCoSlash, Kind: sessionbackupv1.KindParsedSessionRecord,
		SourceKey: record.RevisionID, MediaType: "application/json", Encoding: sessionbackupv1.EncodingIdentity,
	}, recordBytes); err != nil {
		return err
	}
	changeIndex := 0
	for _, edit := range record.Session.FileEdits {
		for _, change := range edit.Changes {
			extension, mediaType := ".txt", "text/plain; charset=utf-8"
			if change.Kind == "diff" {
				extension, mediaType = ".patch", "text/x-diff; charset=utf-8"
			}
			name := fmt.Sprintf("%s/change-%06d%s", prefix, changeIndex, extension)
			changeIndex++
			if err := writes.bytes(sessionbackupv1.Artifact{
				LogicalName: name, MemberID: record.SessionID, Source: sessionbackupv1.ArtifactSourceCoSlash,
				Kind: sessionbackupv1.KindExactChangeBody, SourceKey: change.ID, MediaType: mediaType,
				Encoding: sessionbackupv1.EncodingIdentity,
			}, []byte(change.Text)); err != nil {
				return err
			}
		}
	}
	enrichment := sessionbackupv1.Enrichment{Repository: &repository, RepositoryLocalOnly: repositoryLocalOnly}
	if repository == "" {
		enrichment.Repository = nil
	}
	if sourceKind == sessionbackupv1.SourceLocal {
		branch := record.Session.Branch
		if branch == nil {
			branch = session.CurrentBranchContext(ctx, record.Session.WorkingDirectory)
			enrichment.FilesystemFallbackBranch = branch
		}
		if drift := session.BranchDriftContext(ctx, record.Session.WorkingDirectory, branch); drift != nil {
			enrichment.Git = &sessionbackupv1.GitDrift{BaseBranch: drift.BaseBranch, Ahead: drift.Ahead, Behind: drift.Behind}
		}
		value, err := fullsessionrecord.ToSession(record)
		if err != nil {
			return err
		}
		enrichment.LastEditAtMs = session.LatestFileModificationTimeContext(ctx, value.WorkingDirectory, value.FileEdits)
	} else {
		if frozen.Repository != nil {
			enrichment.Repository = frozen.Repository
			enrichment.RepositoryLocalOnly = frozen.RepositoryLocalOnly
		}
		if record.Session.Branch == nil {
			enrichment.FilesystemFallbackBranch = frozen.Branch
		}
		if frozen.Git != nil {
			enrichment.Git = &sessionbackupv1.GitDrift{BaseBranch: frozen.Git.BaseBranch, Ahead: frozen.Git.Ahead, Behind: frozen.Git.Behind}
		}
		enrichment.LastEditAtMs = frozen.LastEditAt
	}
	enrichmentBytes, err := sessionbackupv1.MarshalEnrichment(enrichment)
	if err != nil {
		return err
	}
	if err := writes.bytes(sessionbackupv1.Artifact{
		LogicalName: prefix + "/enrichment.json", MemberID: record.SessionID,
		Source: sessionbackupv1.ArtifactSourceCoSlash, Kind: sessionbackupv1.KindSessionEnrichment,
		SourceKey: "session-overlay", MediaType: "application/json", Encoding: sessionbackupv1.EncodingIdentity,
	}, enrichmentBytes); err != nil {
		return err
	}
	if persisted.Revision > 0 {
		data, err := sessionbackupv1.MarshalSynthesisRecord(persisted)
		if err != nil {
			return err
		}
		return writes.bytes(sessionbackupv1.Artifact{
			LogicalName: prefix + "/synthesis.json", MemberID: record.SessionID,
			Source: sessionbackupv1.ArtifactSourceCoSlash, Kind: sessionbackupv1.KindSynthesis,
			SourceKey: "persisted-synthesis", MediaType: "application/json", Encoding: sessionbackupv1.EncodingIdentity,
		}, data)
	}
	return nil
}

func (writes *artifactWriter) stream(ctx context.Context, artifact sessionbackupv1.Artifact, open func() (io.ReadCloser, error)) error {
	if writes.totalBytes >= sessionbackupv1.MaxTotalBytes {
		return errArtifactTooLarge
	}
	if len(writes.artifacts) >= sessionbackupv1.MaxArtifacts {
		return sessionbackupv1.ErrInvalid
	}
	destination := filepath.Join(writes.root, filepath.FromSlash(artifact.LogicalName))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	source, err := open()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		source.Close()
		return err
	}
	hash := sha256.New()
	maximum := min(sessionbackupv1.MaxArtifactBytes, sessionbackupv1.MaxTotalBytes-writes.totalBytes)
	bounded := &boundedWriter{writer: io.MultiWriter(output, hash), remaining: maximum}
	count, copyErr := io.CopyBuffer(bounded, &contextReader{ctx: ctx, reader: source}, make([]byte, 128*1024))
	outputErr := output.Close()
	sourceErr := source.Close()
	if copyErr != nil || outputErr != nil || sourceErr != nil {
		return errors.Join(copyErr, outputErr, sourceErr)
	}
	if count > maximum {
		return errArtifactTooLarge
	}
	writes.evidence[artifact.LogicalName] = sessionbackupv1.ArtifactEvidence{ByteLength: count, SHA256: hex.EncodeToString(hash.Sum(nil))}
	writes.artifacts = append(writes.artifacts, artifact)
	writes.totalBytes += count
	return nil
}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (writer *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) <= writer.remaining {
		written, err := writer.writer.Write(data)
		writer.remaining -= int64(written)
		return written, err
	}
	if writer.remaining == 0 {
		return 0, errArtifactTooLarge
	}
	written, err := writer.writer.Write(data[:writer.remaining])
	writer.remaining -= int64(written)
	if err != nil {
		return written, err
	}
	return written, errArtifactTooLarge
}

func (writes *artifactWriter) bytes(artifact sessionbackupv1.Artifact, data []byte) error {
	return writes.stream(writes.ctx, artifact, func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

type snapshotSource struct {
	vendors.ReadSource
	files   map[string]string
	infos   map[string]fs.FileInfo
	missing map[string]bool
}

func (source snapshotSource) Open(name string) (io.ReadCloser, error) {
	if source.missing[name] {
		return nil, fs.ErrNotExist
	}
	if frozen := source.files[name]; frozen != "" {
		return os.Open(frozen)
	}
	return source.ReadSource.Open(name)
}

func (source snapshotSource) Stat(name string) (fs.FileInfo, error) {
	if source.missing[name] {
		return nil, fs.ErrNotExist
	}
	if info := source.infos[name]; info != nil {
		return info, nil
	}
	if frozen := source.files[name]; frozen != "" {
		return os.Stat(frozen)
	}
	return source.ReadSource.Stat(name)
}

func (reader *contextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(destination)
}

func hashStrings(values []string) string {
	sort.Strings(values)
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte(value))
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func withinKnownBounds(fingerprints []vendors.FileFingerprint) bool {
	var total int64
	for _, fingerprint := range fingerprints {
		if fingerprint.Size < 0 || fingerprint.Size > sessionbackupv1.MaxArtifactBytes || total > sessionbackupv1.MaxTotalBytes-fingerprint.Size {
			return false
		}
		total += fingerprint.Size
	}
	return true
}

func exceedsKnownBounds(fingerprints []vendors.FileFingerprint) bool {
	var total int64
	for _, fingerprint := range fingerprints {
		if fingerprint.Size > sessionbackupv1.MaxArtifactBytes || fingerprint.Size >= 0 && total > sessionbackupv1.MaxTotalBytes-fingerprint.Size {
			return true
		}
		if fingerprint.Size >= 0 {
			total += fingerprint.Size
		}
	}
	return false
}
