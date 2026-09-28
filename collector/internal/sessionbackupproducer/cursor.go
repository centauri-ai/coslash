package sessionbackupproducer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/cursor"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func (manager *Manager) captureCursor(ctx context.Context, staging string, selection Selection, handle SourceHandle) (*Prepared, error) {
	if selection.SourceKind != sessionbackupv1.SourceLocal {
		return nil, captureFailure(sessionbackupv1.ProblemUnsupported, "", false)
	}
	plan, err := cursor.PlanBackupContext(ctx, handle.Home, selection.SessionID)
	if err != nil {
		if errors.Is(err, cursor.ErrBackupUnstable) {
			return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawMetadataRows, true)
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
		}
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawMetadataRows, false)
	}
	paths := make([]string, 0)
	for _, files := range plan.Files {
		paths = append(paths, files...)
	}
	sort.Strings(paths)
	if len(paths) == 0 || len(plan.Files) > sessionbackupv1.MaxMembers {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawTranscript, false)
	}
	before, err := vendors.FingerprintSourceFilesContext(ctx, handle.Source, handle.Home, paths)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
	}
	if !withinKnownBounds(before) {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawTranscript, false)
	}
	writes := &artifactWriter{root: staging, ctx: ctx, evidence: map[string]sessionbackupv1.ArtifactEvidence{}}
	frozenFiles := map[string]string{}
	rawEvidence := map[string][]string{}
	rolloutIndex := map[string]int{}
	for _, sourcePath := range paths {
		id := cursor.IDFromPath(sourcePath)
		index := rolloutIndex[id]
		rolloutIndex[id]++
		name := fmt.Sprintf("members/%s/raw/transcript-%06d.jsonl", id, index)
		artifact := sessionbackupv1.Artifact{
			LogicalName: name, MemberID: id, Source: sessionbackupv1.ArtifactSourceCursor,
			Kind: sessionbackupv1.KindRawTranscript, SourceKey: fmt.Sprintf("transcript-%06d", index),
			MediaType: "application/x-ndjson", Encoding: sessionbackupv1.EncodingIdentity,
		}
		if err := writes.stream(ctx, artifact, func() (io.ReadCloser, error) { return handle.Source.Open(sourcePath) }); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
		}
		frozenFiles[sourcePath] = filepath.Join(staging, filepath.FromSlash(name))
		rawEvidence[id] = append(rawEvidence[id], writes.evidence[name].SHA256)
	}
	for id, rows := range plan.Rows {
		for _, projection := range rows {
			name := fmt.Sprintf("members/%s/raw/%s.json", id, projection.SourceKey)
			artifact := sessionbackupv1.Artifact{
				LogicalName: name, MemberID: id, Source: sessionbackupv1.ArtifactSourceCursor,
				Kind: sessionbackupv1.KindRawMetadataRows, SourceKey: projection.SourceKey,
				MediaType: "application/json", Encoding: sessionbackupv1.EncodingIdentity,
			}
			if err := writes.bytes(artifact, projection.Bytes); err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawMetadataRows, false)
			}
			rawEvidence[id] = append(rawEvidence[id], writes.evidence[name].SHA256)
		}
	}
	for id, data := range plan.Sidecars {
		name := fmt.Sprintf("members/%s/raw/meta.json", id)
		if err := writes.bytes(sessionbackupv1.Artifact{
			LogicalName: name, MemberID: id, Source: sessionbackupv1.ArtifactSourceCursor,
			Kind: sessionbackupv1.KindRawSidecar, SourceKey: "cli-meta", MediaType: "application/json", Encoding: sessionbackupv1.EncodingIdentity,
		}, data); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawSidecar, false)
		}
		rawEvidence[id] = append(rawEvidence[id], writes.evidence[name].SHA256)
	}
	if manager.afterRawCopy != nil {
		manager.afterRawCopy()
	}
	after, err := vendors.FingerprintSourceFilesFreshContext(ctx, handle.Source, handle.Home, paths)
	if err != nil || !reflect.DeepEqual(before, after) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	}
	if !plan.Stable() {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawMetadataRows, true)
	}
	frozenSource := snapshotSource{ReadSource: handle.Source, files: frozenFiles}
	parsed, err := cursor.ParseBackupFilesContext(ctx, frozenSource, plan.Files, plan.Metadata)
	if err != nil || len(parsed) != len(plan.Files) {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	records, err := fullsessionrecord.FromParsedFamilyContext(ctx, selection.SourceID, vendors.AgentCursor, frozenSource, parsed, plan.Metadata)
	if err != nil || len(records) != len(plan.Files) {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	byID := make(map[string]fullsessionv1.Record, len(records))
	for _, record := range records {
		if _, exists := byID[record.SessionID]; exists || plan.Files[record.SessionID] == nil ||
			record.Session.Entrypoint == nil || *record.Session.Entrypoint != plan.Entrypoint {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
		}
		byID[record.SessionID] = record
	}
	root, ok := byID[selection.SessionID]
	if !ok || root.ParentSessionID != "" {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
	}
	repository, localOnly := repositoryIdentity(ctx, selection.SourceKind, root.Session.WorkingDirectory, handle.Enrichment[selection.SessionID])
	if repository == "" {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSessionEnrichment, false)
	}
	members := make([]sessionbackupv1.Member, 0, len(records))
	sourceRevisions := make([]string, 0, len(records))
	for id, record := range byID {
		revision := hashStrings(rawEvidence[id])
		sourceRevisions = append(sourceRevisions, id+":"+revision)
		members = append(members, sessionbackupv1.Member{MemberID: id, ParentMemberID: record.ParentSessionID, SourceRevision: revision})
		if err := writes.addProcessed(ctx, record, selection.SourceKind, repository, localOnly, handle.Enrichment[id], sessionbackupv1.SynthesisRecord{}); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
		}
	}
	manifest := sessionbackupv1.Manifest{
		RequiredVersions: []string{sessionbackupv1.ParsedRecordVersion, sessionbackupv1.DatabaseRowsVersion, sessionbackupv1.SchemaVersion},
		Source:           sessionbackupv1.SourceIdentity{Kind: selection.SourceKind, SourceID: selection.SourceID, Agent: vendors.AgentCursor, SourceRevision: hashStrings(sourceRevisions)},
		Repository:       sessionbackupv1.RepositoryIdentity{Canonical: repository, VCS: "git"},
		Producer:         sessionbackupv1.ProducerIdentity{Name: "coslash", Version: manager.collectorVersion, ParserVersion: manager.parserVersion},
		Family:           sessionbackupv1.FamilyIdentity{FamilyID: selection.SessionID, RootMemberID: selection.SessionID},
		Members:          members, Artifacts: writes.artifacts,
	}
	frozen, err := sessionbackupv1.FreezeEvidence(manifest, writes.evidence)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
	}
	data, err := sessionbackupv1.Marshal(frozen)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
	}
	if err := os.WriteFile(filepath.Join(staging, sessionbackupv1.ManifestFileName), data, 0o600); err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnavailable, "", true)
	}
	if _, err := sessionbackupv1.VerifyDirectory(staging); err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
	}
	return &Prepared{BundleID: frozen.CompleteBackupSHA256, Selection: selection, Manifest: frozen, Coverage: coverage(frozen)}, nil
}
