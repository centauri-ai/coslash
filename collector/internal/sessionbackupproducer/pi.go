package sessionbackupproducer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type piFrozen struct {
	writes *artifactWriter
	files  map[string]string
	infos  []fs.FileInfo
	parts  []string
}

// PiSourceRevision shares capture's bounded frozen bytes and portable selection.
// The caller supplies inputs from its discovery/runtime snapshot.
func PiSourceRevision(ctx context.Context, inputs pi.SessionInputs) (revision string, err error) {
	defer func() {
		if ctx.Err() != nil {
			err = ctx.Err()
			return
		}
		var failure *captureError
		if errors.As(err, &failure) {
			_, err = captureProblem(Selection{SourceKind: sessionbackupv1.SourceLocal, Agent: vendors.AgentPi, SessionID: inputs.ID}, failure)
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp("", "coslash-pi-revision-*")
	if err != nil {
		return "", captureFailure(sessionbackupv1.ProblemUnavailable, "", true)
	}
	defer os.RemoveAll(staging)
	frozen, err := freezePiInputs(ctx, staging, inputs)
	if err != nil {
		return "", err
	}
	_, revision, err = frozen.project(ctx, inputs)
	if err != nil {
		return "", frozen.projectionFailure(ctx, inputs, err)
	}
	if err := frozen.verify(ctx, inputs); err != nil {
		return "", err
	}
	return revision, nil
}

func freezePiInputs(ctx context.Context, staging string, inputs pi.SessionInputs) (*piFrozen, error) {
	if len(inputs.Files) == 0 || len(inputs.Files) > 257 || inputs.Files[0] != inputs.Path {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawTranscript, false)
	}
	frozen := &piFrozen{
		writes: &artifactWriter{root: staging, ctx: ctx, evidence: map[string]sessionbackupv1.ArtifactEvidence{}},
		files:  map[string]string{}, parts: []string{"pi-source-v1"},
	}
	for i, path := range inputs.Files {
		file, info, err := openPiInput(ctx, path)
		if err != nil {
			return nil, sourceReadFailure(err, sessionbackupv1.KindRawTranscript)
		}
		name := fmt.Sprintf("members/root/raw/transcript-%06d.jsonl", i)
		artifact := sessionbackupv1.Artifact{
			LogicalName: name, MemberID: inputs.ID, Source: sessionbackupv1.ArtifactSourcePi,
			Kind: sessionbackupv1.KindRawTranscript, SourceKey: fmt.Sprintf("transcript-%06d", i),
			MediaType: "application/x-ndjson", Encoding: sessionbackupv1.EncodingIdentity,
		}
		if err := frozen.writes.stream(ctx, artifact, func() (io.ReadCloser, error) { return file, nil }); err != nil {
			// stream may reject a known budget before opening its reader.
			_ = file.Close()
			return nil, sourceReadFailure(err, sessionbackupv1.KindRawTranscript)
		}
		frozen.files[path] = filepath.Join(staging, filepath.FromSlash(name))
		frozen.infos = append(frozen.infos, info)
		evidence := frozen.writes.evidence[name]
		frozen.parts = append(frozen.parts, fmt.Sprintf("input:%06d:%d:%s", i, evidence.ByteLength, evidence.SHA256))
	}
	return frozen, nil
}

func openPiInput(ctx context.Context, path string) (*os.File, fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, vendors.ErrInvalidData
	}
	if info.Size() > sessionbackupv1.MaxArtifactBytes {
		return nil, nil, errArtifactTooLarge
	}
	// Nonblocking open also rejects a FIFO replacement between stat and open.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || ctx.Err() != nil {
		file.Close()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, vendors.ErrInvalidData
	}
	return file, opened, nil
}

func (frozen *piFrozen) project(ctx context.Context, inputs pi.SessionInputs) (fullsessionv1.Record, string, error) {
	parsed, selection, err := pi.ParseFrozenSessionContext(ctx, inputs, frozen.files)
	if err != nil {
		return fullsessionv1.Record{}, "", processedCaptureFailure(err, sessionbackupv1.KindParsedSessionRecord)
	}
	records, err := fullsessionrecord.FromParsedFamilyContext(ctx, "pi-source-revision", vendors.AgentPi, vendors.LocalReadSource, []*vendors.ParsedSession{parsed}, vendors.EmptySessionMetadata())
	if err != nil || len(records) != 1 || records[0].SessionID != inputs.ID || records[0].ParentSessionID != "" || len(records[0].Session.Subagents) != 0 {
		return fullsessionv1.Record{}, "", processedCaptureFailure(err, sessionbackupv1.KindParsedSessionRecord)
	}
	parts := append([]string(nil), frozen.parts...)
	parts = append(parts, fmt.Sprintf("context:%t:%s", selection.Verified, selection.LeafID), "portable:"+records[0].RevisionID)
	return records[0], hashStrings(parts), nil
}

func (frozen *piFrozen) projectionFailure(ctx context.Context, inputs pi.SessionInputs, failure error) error {
	unstable := captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	plan, err := pi.PlanSourcesContext(ctx)
	if err != nil {
		return unstable
	}
	current, err := plan.SessionInputsContext(ctx, inputs.ID)
	if err != nil || current.ID != inputs.ID || current.Path != inputs.Path || !slices.Equal(current.Files, inputs.Files) {
		return unstable
	}
	// A permanent failure must also survive fresh planning over the same bytes.
	if _, _, err := frozen.project(ctx, current); err == nil {
		return unstable
	}
	if err := frozen.verify(ctx, inputs); err != nil {
		return err
	}
	return failure
}

func (frozen *piFrozen) verify(ctx context.Context, inputs pi.SessionInputs) error {
	for i, path := range inputs.Files {
		file, info, err := openPiInput(ctx, path)
		if err != nil {
			return captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: file}, sessionbackupv1.MaxArtifactBytes+1))
		closeErr := file.Close()
		evidence := frozen.writes.evidence[frozen.writes.artifacts[i].LogicalName]
		before := frozen.infos[i]
		if readErr != nil || closeErr != nil || !os.SameFile(before, info) || before.Size() != info.Size() || !before.ModTime().Equal(info.ModTime()) || n != evidence.ByteLength || hex.EncodeToString(hash.Sum(nil)) != evidence.SHA256 {
			return captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
		}
	}
	return nil
}

func (manager *Manager) capturePi(ctx context.Context, staging string, selection Selection, handle SourceHandle) (*Prepared, error) {
	if handle.Source != vendors.LocalReadSource {
		return nil, captureFailure(sessionbackupv1.ProblemUnsupported, "", false)
	}
	plan, err := pi.PlanSourcesContext(ctx)
	if err != nil {
		return nil, sourceReadFailure(err, sessionbackupv1.KindRawTranscript)
	}
	inputs, err := plan.SessionInputsContext(ctx, selection.SessionID)
	if err != nil {
		return nil, sourceReadFailure(err, sessionbackupv1.KindRawTranscript)
	}
	frozen, err := freezePiInputs(ctx, staging, inputs)
	if err != nil {
		return nil, err
	}
	record, revision, err := frozen.project(ctx, inputs)
	if err != nil {
		return nil, frozen.projectionFailure(ctx, inputs, err)
	}
	if manager.afterRawCopy != nil {
		manager.afterRawCopy()
	}
	freshRuntime, _ := pi.LoadRuntimeSnapshotContext(ctx)
	currentPlan, err := pi.PlanSourcesContext(pi.WithRuntimeSnapshot(ctx, freshRuntime))
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	}
	current, err := currentPlan.SessionInputsContext(ctx, selection.SessionID)
	if err != nil || current.ID != inputs.ID || current.Path != inputs.Path || !slices.Equal(current.Files, inputs.Files) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	}
	if err := frozen.verify(ctx, inputs); err != nil {
		return nil, err
	}
	_, currentRevision, err := frozen.project(ctx, current)
	if err != nil || revision != currentRevision {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	}
	record.SourceID = selection.SourceID
	record, err = fullsessionv1.Freeze(record)
	if err != nil {
		return nil, processedCaptureFailure(err, sessionbackupv1.KindParsedSessionRecord)
	}
	repository, localOnly := repositoryIdentity(ctx, selection.SourceKind, record.Session.WorkingDirectory, handle.Enrichment[selection.SessionID])
	member := sessionbackupv1.Member{MemberID: selection.SessionID, SourceRevision: revision}
	persisted := sessionbackupv1.SynthesisRecord{}
	if manager.synthesis != nil {
		cached, loadErr := manager.synthesis.LoadRecord(vendors.AgentPi, selection.SessionID)
		if loadErr != nil && !errors.Is(loadErr, fs.ErrNotExist) {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSynthesis, false)
		}
		if loadErr == nil && cached.Revision == record.Session.LastActivityAtMs {
			persisted = sessionbackupv1.SynthesisRecord{Agent: vendors.AgentPi, SessionID: selection.SessionID, Revision: cached.Revision, Model: cached.Model, GeneratedAt: cached.GeneratedAt, Synthesis: fullsessionv1.SessionSynthesis{Goals: append([]string(nil), cached.Synthesis.Goals...), Outcome: cached.Synthesis.Outcome, KeyDecisions: append([]string(nil), cached.Synthesis.KeyDecisions...), NextStep: cached.Synthesis.NextStep}}
			member.SynthesisRevisionMs = cached.Revision
		}
	}
	if err := frozen.writes.addProcessed(ctx, record, handle.Home, selection.SourceKind, repository, localOnly, handle.Enrichment[selection.SessionID], persisted); err != nil {
		return nil, processedCaptureFailure(err, "")
	}
	manifest, err := sessionbackupv1.FreezeEvidence(sessionbackupv1.Manifest{
		RequiredVersions: []string{sessionbackupv1.ParsedRecordVersion, sessionbackupv1.SchemaVersion},
		Source:           sessionbackupv1.SourceIdentity{Kind: selection.SourceKind, SourceID: selection.SourceID, Agent: vendors.AgentPi, SourceRevision: revision},
		Repository:       manifestRepository(repository),
		Producer:         sessionbackupv1.ProducerIdentity{Name: "coslash", Version: manager.collectorVersion, ParserVersion: manager.parserVersion},
		Family:           sessionbackupv1.FamilyIdentity{FamilyID: selection.SessionID, RootMemberID: selection.SessionID},
		Members:          []sessionbackupv1.Member{member}, Artifacts: frozen.writes.artifacts,
	}, frozen.writes.evidence)
	if err != nil {
		return nil, processedCaptureFailure(err, "")
	}
	data, err := sessionbackupv1.Marshal(manifest)
	if err != nil {
		return nil, processedCaptureFailure(err, "")
	}
	if err := os.WriteFile(filepath.Join(staging, sessionbackupv1.ManifestFileName), data, 0o600); err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnavailable, "", true)
	}
	return &Prepared{BundleID: manifest.CompleteBackupSHA256, Selection: selection, Manifest: manifest, Coverage: coverage(manifest)}, nil
}
