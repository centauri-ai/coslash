package sessionbackupproducer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
	backup "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func (manager *Manager) captureOpenCode(ctx context.Context, staging string, selection Selection) (*Prepared, error) {
	captured, err := opencode.CaptureFamilyContext(ctx, selection.SessionID)
	if err != nil {
		switch {
		case errors.Is(err, opencode.ErrUnattributable):
			return nil, captureFailure(backup.ProblemUnattributable, backup.KindRawMetadataRows, false)
		case errors.Is(err, opencode.ErrUnstable):
			return nil, captureFailure(backup.ProblemUnstable, backup.KindRawMetadataRows, true)
		case errors.Is(err, backup.ErrInvalid):
			// The family's rows break the rows contract (for example a value
			// over its size limit); reading again yields the same rows.
			return nil, captureFailure(backup.ProblemInvalid, backup.KindRawMetadataRows, false)
		case errors.Is(err, os.ErrNotExist), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil, captureFailure(backup.ProblemUnavailable, backup.KindRawMetadataRows, true)
		default:
			return nil, captureFailure(backup.ProblemUnreadable, backup.KindRawMetadataRows, true)
		}
	}
	defer captured.Close()
	if manager.afterRawCopy != nil {
		manager.afterRawCopy()
	}
	if !captured.Stable(ctx) {
		return nil, captureFailure(backup.ProblemUnstable, backup.KindRawMetadataRows, true)
	}
	records, err := fullsessionrecord.FromParsedFamilyContext(ctx, selection.SourceID, vendors.AgentOpenCode, vendors.LocalReadSource, captured.Parsed, vendors.EmptySessionMetadata())
	if err != nil {
		return nil, captureFailure(backup.ProblemInvalid, backup.KindParsedSessionRecord, false)
	}
	if len(records) != len(captured.Rows) || len(records) > backup.MaxMembers {
		return nil, captureFailure(backup.ProblemUnattributable, backup.KindParsedSessionRecord, false)
	}
	writes := &artifactWriter{root: staging, ctx: ctx, evidence: map[string]backup.ArtifactEvidence{}}
	recordByID := make(map[string]fullsessionv1.Record, len(records))
	for _, record := range records {
		if _, duplicate := recordByID[record.SessionID]; duplicate {
			return nil, captureFailure(backup.ProblemUnattributable, backup.KindParsedSessionRecord, false)
		}
		recordByID[record.SessionID] = record
	}
	root, ok := recordByID[selection.SessionID]
	if !ok {
		return nil, captureFailure(backup.ProblemUnattributable, backup.KindParsedSessionRecord, false)
	}
	repository, repositoryLocalOnly := repositoryIdentity(ctx, selection.SourceKind, root.Session.WorkingDirectory, remote.BackupSessionEnrichment{})
	if repository == "" {
		return nil, captureFailure(backup.ProblemInvalid, backup.KindSessionEnrichment, false)
	}
	members := make([]backup.Member, 0, len(records))
	sourceRevisions := make([]string, 0, len(records))
	for _, record := range records {
		rows, exists := captured.Rows[record.SessionID]
		if !exists {
			return nil, captureFailure(backup.ProblemUnattributable, backup.KindRawMetadataRows, false)
		}
		name := fmt.Sprintf("members/%s/raw/opencode-rows.json", record.SessionID)
		if err := writes.bytes(backup.Artifact{
			LogicalName: name, MemberID: record.SessionID, Source: backup.ArtifactSourceOpenCode,
			Kind: backup.KindRawMetadataRows, SourceKey: "opencode", MediaType: "application/json", Encoding: backup.EncodingIdentity,
		}, rows); err != nil {
			if errors.Is(err, errArtifactTooLarge) {
				return nil, captureTooLarge(backup.KindRawMetadataRows)
			}
			return nil, captureFailure(backup.ProblemInvalid, backup.KindRawMetadataRows, false)
		}
		revision := writes.evidence[name].SHA256
		members = append(members, backup.Member{MemberID: record.SessionID, ParentMemberID: record.ParentSessionID, SourceRevision: revision})
		sourceRevisions = append(sourceRevisions, record.SessionID+":"+revision)
		if err := writes.addProcessed(ctx, record, selection.SourceKind, repository, repositoryLocalOnly, remote.BackupSessionEnrichment{}, backup.SynthesisRecord{}); err != nil {
			return nil, processedCaptureFailure(err, "")
		}
	}
	if !captured.Stable(ctx) {
		return nil, captureFailure(backup.ProblemUnstable, backup.KindRawMetadataRows, true)
	}
	sort.Strings(sourceRevisions)
	manifest := backup.Manifest{
		RequiredVersions: []string{backup.ParsedRecordVersion, backup.DatabaseRowsVersion, backup.SchemaVersion},
		Source:           backup.SourceIdentity{Kind: selection.SourceKind, SourceID: selection.SourceID, Agent: vendors.AgentOpenCode, SourceRevision: hashStrings(sourceRevisions)},
		Repository:       backup.RepositoryIdentity{Canonical: repository, VCS: "git"},
		Producer:         backup.ProducerIdentity{Name: "coslash", Version: manager.collectorVersion, ParserVersion: manager.parserVersion},
		Family:           backup.FamilyIdentity{FamilyID: selection.SessionID, RootMemberID: selection.SessionID},
		Members:          members, Artifacts: writes.artifacts,
	}
	frozen, err := backup.FreezeEvidence(manifest, writes.evidence)
	if err != nil {
		return nil, captureFailure(backup.ProblemInvalid, "", false)
	}
	manifestBytes, err := backup.Marshal(frozen)
	if err != nil {
		return nil, captureFailure(backup.ProblemInvalid, "", false)
	}
	if err := os.WriteFile(filepath.Join(staging, backup.ManifestFileName), manifestBytes, 0o600); err != nil {
		return nil, captureFailure(backup.ProblemUnavailable, "", true)
	}
	return &Prepared{BundleID: frozen.CompleteBackupSHA256, Selection: selection, Manifest: frozen, Coverage: coverage(frozen)}, nil
}
