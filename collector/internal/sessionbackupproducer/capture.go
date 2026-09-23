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
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type artifactWriter struct {
	root      string
	ctx       context.Context
	evidence  map[string]sessionbackupv1.ArtifactEvidence
	artifacts []sessionbackupv1.Artifact
}

func (manager *Manager) Prepare(ctx context.Context, selection Selection) (*Prepared, error) {
	if selection.Agent != vendors.AgentCodex {
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

	handle, err := manager.openSource(ctx, selection)
	if err != nil || handle.Source == nil || handle.Home == "" {
		return problem(selection, sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
	}
	if handle.Close != nil {
		defer handle.Close()
	}
	prepared, err := manager.capture(ctx, staging, selection, handle)
	if err != nil {
		var captureErr *captureError
		if errors.As(err, &captureErr) {
			return problem(selection, captureErr.code, captureErr.kind, captureErr.retryable)
		}
		return problem(selection, sessionbackupv1.ProblemInvalid, "", false)
	}

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
	keep = true
	return prepared, nil
}

type captureError struct {
	code      string
	kind      string
	retryable bool
}

func (err *captureError) Error() string { return err.code }

func captureFailure(code, kind string, retryable bool) error {
	return &captureError{code: code, kind: kind, retryable: retryable}
}

func (manager *Manager) capture(ctx context.Context, staging string, selection Selection, handle SourceHandle) (*Prepared, error) {
	activeScan, err := codex.ScanSource(handle.Source, codex.SessionsRoot(handle.Home))
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
	}
	archivedScan, err := codex.ScanSource(handle.Source, codex.ArchivedDir(handle.Home))
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
	}
	for _, skipped := range append(activeScan.Skipped, archivedScan.Skipped...) {
		if codex.SessionIDFromRollout(skipped.Path) == selection.SessionID {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
		}
	}
	allFiles := append(append([]string(nil), activeScan.Files...), archivedScan.Files...)
	headers := codex.HeadersSource(handle.Source, allFiles)
	roots := codex.FamilyRoots(headers)
	var familyFiles []string
	for _, file := range allFiles {
		if roots[file] == selection.SessionID {
			if headers[file].Err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
			}
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
	before, err := vendors.FingerprintSourceFiles(handle.Source, handle.Home, familyFiles)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
	}

	memberIDs := map[string]bool{}
	for _, file := range familyFiles {
		memberIDs[headers[file].SessionID] = true
	}
	indexPath := codex.SessionIndexPath(handle.Home)
	var indexBefore []vendors.FileFingerprint
	if _, statErr := handle.Source.Stat(indexPath); statErr == nil {
		indexBefore, err = vendors.FingerprintSourceFiles(handle.Source, handle.Home, []string{indexPath})
		if err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawSidecar, true)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawSidecar, true)
	}
	indexRows, indexPresent, err := codex.ReadSessionIndexRows(handle.Source, handle.Home, memberIDs)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawSidecar, false)
	}
	if indexPresent != (len(indexBefore) == 1) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawSidecar, true)
	}
	metadata := metadataFromRows(indexRows)
	parsed, err := codex.ParseFamilyFilesSource(handle.Source, handle.Home, familyFiles, activeScan.Files)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	if len(parsed) != len(memberIDs) {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
	}
	records, err := fullsessionrecord.FromParsedFamily(selection.SourceID, vendors.AgentCodex, handle.Source, parsed, metadata)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	synthesisRecords := map[string]sessionbackupv1.SynthesisRecord{}
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

	repository, repositoryLocalOnly := repositoryIdentity(selection.SourceKind, rootRecord.Session.WorkingDirectory)
	if repository == "" {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSessionEnrichment, false)
	}
	writes := &artifactWriter{root: staging, ctx: ctx, evidence: map[string]sessionbackupv1.ArtifactEvidence{}}
	rawEvidenceByMember := map[string][]string{}
	frozenFiles := map[string]string{}
	rolloutIndex := map[string]int{}
	for _, file := range familyFiles {
		if err := ctx.Err(); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
		}
		memberID := headers[file].SessionID
		index := rolloutIndex[memberID]
		rolloutIndex[memberID]++
		name := fmt.Sprintf("members/%s/raw/rollout-%06d.jsonl", memberID, index)
		artifact := sessionbackupv1.Artifact{
			LogicalName: name, MemberID: memberID, Source: sessionbackupv1.ArtifactSourceCodex,
			Kind: sessionbackupv1.KindRawTranscript, SourceKey: fmt.Sprintf("rollout-%06d", index),
			MediaType: "application/x-ndjson", Encoding: sessionbackupv1.EncodingIdentity,
		}
		if err := writes.stream(ctx, artifact, func() (io.ReadCloser, error) { return handle.Source.Open(file) }); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
		}
		frozenFiles[file] = filepath.Join(staging, filepath.FromSlash(name))
		rawEvidenceByMember[memberID] = append(rawEvidenceByMember[memberID], writes.evidence[name].SHA256)
	}
	for memberID, row := range indexRows {
		name := fmt.Sprintf("members/%s/raw/session-index-row.jsonl", memberID)
		artifact := sessionbackupv1.Artifact{
			LogicalName: name, MemberID: memberID, Source: sessionbackupv1.ArtifactSourceCodex,
			Kind: sessionbackupv1.KindRawSidecar, SourceKey: "session_index", MediaType: "application/x-ndjson",
			Encoding: sessionbackupv1.EncodingIdentity,
		}
		if err := writes.bytes(artifact, row); err != nil {
			return nil, err
		}
		rawEvidenceByMember[memberID] = append(rawEvidenceByMember[memberID], writes.evidence[name].SHA256)
	}
	if manager.afterRawCopy != nil {
		manager.afterRawCopy()
	}
	after, err := vendors.FingerprintSourceFilesFresh(handle.Source, handle.Home, familyFiles)
	if err != nil || !reflect.DeepEqual(before, after) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	}
	if indexPresent {
		indexAfter, statErr := vendors.FingerprintSourceFilesFresh(handle.Source, handle.Home, []string{indexPath})
		if statErr != nil || !reflect.DeepEqual(indexBefore, indexAfter) {
			return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawSidecar, true)
		}
	}
	// Reparse from the exact frozen rollout bytes. The initial parse establishes
	// that the selected live family is complete; this parse binds every
	// processed artifact to the bytes that will actually be uploaded.
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
	parsed, err = codex.ParseFamilyFilesSource(frozenSource, handle.Home, familyFiles, activeFamilyFiles)
	if err != nil || len(parsed) != len(memberIDs) {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	records, err = fullsessionrecord.FromParsedFamily(selection.SourceID, vendors.AgentCodex, frozenSource, parsed, metadata)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	if selection.SourceKind == sessionbackupv1.SourceLocal && manager.synthesis != nil {
		revisionByID := map[string]int64{}
		for _, record := range records {
			revisionByID[record.SessionID] = record.Session.LastActivityAtMs
		}
		attached := false
		for _, item := range parsed {
			persisted, loadErr := manager.synthesis.LoadRecord(vendors.AgentCodex, item.Session.ID)
			if errors.Is(loadErr, fs.ErrNotExist) {
				continue
			}
			if loadErr != nil || persisted.Revision != revisionByID[item.Session.ID] {
				return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSynthesis, false)
			}
			item.Session.Synthesis = &persisted.Synthesis
			attached = true
			synthesisRecords[item.Session.ID] = sessionbackupv1.SynthesisRecord{
				Agent: vendors.AgentCodex, SessionID: item.Session.ID, Revision: persisted.Revision,
				Model: persisted.Model, GeneratedAt: persisted.GeneratedAt,
				Synthesis: fullsessionv1.SessionSynthesis{
					Goals: append([]string(nil), persisted.Synthesis.Goals...), Outcome: persisted.Synthesis.Outcome,
					KeyDecisions: append([]string(nil), persisted.Synthesis.KeyDecisions...), NextStep: persisted.Synthesis.NextStep,
				},
			}
		}
		if attached {
			records, err = fullsessionrecord.FromParsedFamily(selection.SourceID, vendors.AgentCodex, frozenSource, parsed, metadata)
			if err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
			}
		}
	}
	recordByID = make(map[string]fullsessionv1.Record, len(records))
	for _, record := range records {
		if _, duplicate := recordByID[record.SessionID]; duplicate {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
		}
		recordByID[record.SessionID] = record
	}
	rootRecord, ok = recordByID[selection.SessionID]
	if !ok || len(recordByID) != len(memberIDs) {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
	}
	repository, repositoryLocalOnly = repositoryIdentity(selection.SourceKind, rootRecord.Session.WorkingDirectory)
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
		if err := writes.addProcessed(record, selection.SourceKind, repository, repositoryLocalOnly, synthesisRecords[memberID]); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
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

func metadataFromRows(rows map[string][]byte) *vendors.SessionMetadata {
	metadata := vendors.EmptySessionMetadata()
	for id, row := range rows {
		var entry struct {
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal(row, &entry) == nil && entry.ThreadName != "" {
			metadata.Session(id).Name = entry.ThreadName
		}
	}
	return metadata
}

func repositoryIdentity(sourceKind, workingDirectory string) (string, bool) {
	if sourceKind == sessionbackupv1.SourceLocal {
		return session.CanonicalRepositoryName(workingDirectory)
	}
	name := path.Base(path.Clean(workingDirectory))
	if name == "." || name == "/" {
		return "", true
	}
	return name, true
}

func (writes *artifactWriter) addProcessed(record fullsessionv1.Record, sourceKind, repository string, repositoryLocalOnly bool, persisted sessionbackupv1.SynthesisRecord) error {
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
	if sourceKind == sessionbackupv1.SourceLocal {
		branch := record.Session.Branch
		if branch == nil {
			branch = session.CurrentBranch(record.Session.WorkingDirectory)
			enrichment.FilesystemFallbackBranch = branch
		}
		if drift := session.BranchDrift(record.Session.WorkingDirectory, branch); drift != nil {
			enrichment.Git = &sessionbackupv1.GitDrift{BaseBranch: drift.BaseBranch, Ahead: drift.Ahead, Behind: drift.Behind}
		}
		value, err := fullsessionrecord.ToSession(record)
		if err != nil {
			return err
		}
		enrichment.LastEditAtMs = session.LatestFileModificationTime(value.WorkingDirectory, value.FileEdits)
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
	count, copyErr := io.CopyBuffer(io.MultiWriter(output, hash), &contextReader{ctx: ctx, reader: source}, make([]byte, 128*1024))
	outputErr := output.Close()
	sourceErr := source.Close()
	if copyErr != nil || outputErr != nil || sourceErr != nil {
		return errors.Join(copyErr, outputErr, sourceErr)
	}
	writes.evidence[artifact.LogicalName] = sessionbackupv1.ArtifactEvidence{ByteLength: count, SHA256: hex.EncodeToString(hash.Sum(nil))}
	writes.artifacts = append(writes.artifacts, artifact)
	return nil
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
	files map[string]string
}

func (source snapshotSource) Open(name string) (io.ReadCloser, error) {
	if frozen := source.files[name]; frozen != "" {
		return os.Open(frozen)
	}
	return source.ReadSource.Open(name)
}

func (source snapshotSource) Stat(name string) (fs.FileInfo, error) {
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
