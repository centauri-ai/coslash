package sessionbackupproducer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type claudeInput struct {
	path, memberID, kind, key, mediaType string
	info                                 fs.FileInfo
}

func (manager *Manager) captureClaude(ctx context.Context, staging string, selection Selection, handle SourceHandle) (*Prepared, error) {
	files, err := claude.CompleteFamilyFilesSourceContext(ctx, handle.Source, handle.Home, selection.SessionID)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
	}
	if len(files) == 0 {
		return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
	}
	sort.Strings(files)
	inputs := make([]claudeInput, 0, len(files)*2)
	missing := map[string]bool{}
	seen := map[string]bool{}
	memberIDs := map[string]bool{}
	rootIDs := map[string]bool{}
	rootFound := false
	add := func(file, memberID, kind, key, mediaType string, required bool) error {
		if seen[file] {
			return nil
		}
		seen[file] = true
		info, err := handle.Source.Stat(file)
		if errors.Is(err, fs.ErrNotExist) && !required {
			missing[file] = true
			return nil
		}
		if err != nil {
			return captureFailure(sessionbackupv1.ProblemUnreadable, kind, true)
		}
		if !claudeInputWithinProjects(file, claude.ProjectsRoot(handle.Home)) {
			return captureFailure(sessionbackupv1.ProblemUnattributable, kind, false)
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > sessionbackupv1.MaxArtifactBytes {
			return captureFailure(sessionbackupv1.ProblemInvalid, kind, false)
		}
		inputs = append(inputs, claudeInput{file, memberID, kind, key, mediaType, info})
		return nil
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindRawTranscript, true)
		}
		memberID := claude.SessionIDFromPath(file)
		parent := claude.ParentIDFromPath(file)
		if !safeClaudeMemberID(memberID) {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
		}
		if memberID == selection.SessionID && parent == "" {
			rootFound = true
		}
		memberIDs[memberID] = true
		if err := add(file, memberID, sessionbackupv1.KindRawTranscript, "transcript-"+fmt.Sprint(len(inputs)), "application/x-ndjson", true); err != nil {
			return nil, err
		}
		if parent == "" {
			rootIDs[memberID] = true
			continue
		}
		if err := add(strings.TrimSuffix(file, ".jsonl")+".meta.json", memberID, sessionbackupv1.KindRawSidecar, "subagent-meta", "application/json", false); err != nil {
			return nil, err
		}
		if strings.Contains(filepath.ToSlash(file), "/subagents/workflows/") {
			runDir := filepath.Dir(strings.Replace(filepath.ToSlash(file), "/subagents/workflows/", "/workflows/", 1))
			if err := add(runDir+".json", memberID, sessionbackupv1.KindRawSidecar, "workflow-state", "application/json", false); err != nil {
				return nil, err
			}
			if err := add(filepath.Join(filepath.Dir(file), "journal.jsonl"), memberID, sessionbackupv1.KindRawSidecar, "workflow-journal", "application/x-ndjson", false); err != nil {
				return nil, err
			}
		}
	}
	if !rootFound || len(memberIDs) > sessionbackupv1.MaxMembers || len(inputs)+2*len(memberIDs) > sessionbackupv1.MaxArtifacts {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawTranscript, false)
	}
	paths := make([]string, 0, len(inputs))
	for _, input := range inputs {
		paths = append(paths, input.path)
	}
	before, err := vendors.FingerprintSourceFilesFreshContext(ctx, handle.Source, handle.Home, paths)
	if err != nil || !withinKnownBounds(before) {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, sessionbackupv1.KindRawTranscript, true)
	}
	writes := &artifactWriter{root: staging, ctx: ctx, evidence: map[string]sessionbackupv1.ArtifactEvidence{}}
	frozenFiles := map[string]string{}
	frozenInfo := map[string]fs.FileInfo{}
	rawHashes := map[string][]string{}
	for index, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnavailable, input.kind, true)
		}
		name := fmt.Sprintf("members/%s/raw/source-%06d", input.memberID, index)
		if input.mediaType == "application/x-ndjson" {
			name += ".jsonl"
		} else {
			name += ".json"
		}
		artifact := sessionbackupv1.Artifact{LogicalName: name, MemberID: input.memberID,
			Source: sessionbackupv1.ArtifactSourceClaude, Kind: input.kind, SourceKey: fmt.Sprintf("%s-%06d", input.key, index),
			MediaType: input.mediaType, Encoding: sessionbackupv1.EncodingIdentity}
		if err := writes.stream(ctx, artifact, func() (io.ReadCloser, error) { return handle.Source.Open(input.path) }); err != nil {
			return nil, sourceReadFailure(err, input.kind)
		}
		frozen := filepath.Join(staging, filepath.FromSlash(name))
		if err := validateClaudeJSON(frozen, input.mediaType == "application/x-ndjson"); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, input.kind, false)
		}
		frozenFiles[input.path] = frozen
		frozenInfo[input.path] = input.info
		rawHashes[input.memberID] = append(rawHashes[input.memberID], writes.evidence[name].SHA256)
	}
	if manager.afterRawCopy != nil {
		manager.afterRawCopy()
	}
	after, err := vendors.FingerprintSourceFilesFreshContext(ctx, handle.Source, handle.Home, paths)
	if err != nil || !reflect.DeepEqual(before, after) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	}
	for file := range missing {
		if _, err := handle.Source.Stat(file); !errors.Is(err, fs.ErrNotExist) {
			return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawSidecar, true)
		}
	}
	currentFiles, err := claude.CompleteFamilyFilesSourceContext(ctx, handle.Source, handle.Home, selection.SessionID)
	if err != nil || !sameClaudeFiles(files, currentFiles) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, sessionbackupv1.KindRawTranscript, true)
	}
	frozenSource := snapshotSource{ReadSource: handle.Source, files: frozenFiles, infos: frozenInfo, missing: missing}
	for _, input := range inputs {
		var err error
		if input.kind == sessionbackupv1.KindRawTranscript {
			err = claude.ValidateCompleteTranscriptSourceContext(ctx, frozenSource, input.path)
		} else {
			err = claude.ValidateCompleteSidecarSourceContext(ctx, frozenSource, input.path, input.key)
		}
		if err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, input.kind, false)
		}
	}
	parsed, err := claude.ParseFamilyFilesSourceContext(ctx, frozenSource, files)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindParsedSessionRecord, false)
	}
	records, err := fullsessionrecord.FromParsedFamilyContext(ctx, selection.SourceID, vendors.AgentClaude, frozenSource, parsed, vendors.EmptySessionMetadata())
	if err != nil || len(records) > len(memberIDs) {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
	}
	recordByID := make(map[string]fullsessionv1.Record, len(records))
	for _, record := range records {
		if !memberIDs[record.SessionID] || recordByID[record.SessionID].SessionID != "" {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
		}
		recordByID[record.SessionID] = record
	}
	for id := range memberIDs {
		if _, represented := recordByID[id]; represented {
			continue
		}
		if !rootIDs[id] || id == selection.SessionID {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
		}
		for index := range writes.artifacts {
			artifact := &writes.artifacts[index]
			if artifact.MemberID != id {
				continue
			}
			oldName := artifact.LogicalName
			artifact.MemberID = selection.SessionID
			artifact.LogicalName = strings.Replace(oldName, "members/"+id+"/", "members/"+selection.SessionID+"/", 1)
			newPath := filepath.Join(staging, filepath.FromSlash(artifact.LogicalName))
			if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemUnavailable, artifact.Kind, true)
			}
			if err := os.Rename(filepath.Join(staging, filepath.FromSlash(oldName)), newPath); err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemUnavailable, artifact.Kind, true)
			}
			writes.evidence[artifact.LogicalName] = writes.evidence[oldName]
			delete(writes.evidence, oldName)
		}
		rawHashes[selection.SessionID] = append(rawHashes[selection.SessionID], rawHashes[id]...)
		delete(rawHashes, id)
	}
	rootRecord, ok := recordByID[selection.SessionID]
	if !ok {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindParsedSessionRecord, false)
	}
	repository, repositoryLocalOnly := repositoryIdentity(ctx, selection.SourceKind, rootRecord.Session.WorkingDirectory, handle.Enrichment[selection.SessionID])
	if repository == "" {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSessionEnrichment, false)
	}
	synthesisRecords := map[string]sessionbackupv1.SynthesisRecord{}
	if manager.synthesis != nil {
		for id, record := range recordByID {
			if err := ctx.Err(); err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemUnavailable, sessionbackupv1.KindSynthesis, true)
			}
			persisted, loadErr := manager.synthesis.LoadRecord(vendors.AgentClaude, id)
			if errors.Is(loadErr, fs.ErrNotExist) {
				continue
			}
			if loadErr != nil {
				return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSynthesis, false)
			}
			if persisted.Revision != record.Session.LastActivityAtMs {
				continue
			}
			synthesisRecords[id] = sessionbackupv1.SynthesisRecord{
				Agent: vendors.AgentClaude, SessionID: id, Revision: persisted.Revision,
				Model: persisted.Model, GeneratedAt: persisted.GeneratedAt,
				Synthesis: fullsessionv1.SessionSynthesis{Goals: append([]string(nil), persisted.Synthesis.Goals...),
					Outcome: persisted.Synthesis.Outcome, KeyDecisions: append([]string(nil), persisted.Synthesis.KeyDecisions...), NextStep: persisted.Synthesis.NextStep},
			}
		}
	}
	members := make([]sessionbackupv1.Member, 0, len(records))
	revisionInput := make([]string, 0, len(records))
	for id, record := range recordByID {
		revision := hashStrings(rawHashes[id])
		member := sessionbackupv1.Member{MemberID: id, ParentMemberID: record.ParentSessionID, SourceRevision: revision}
		if persisted, ok := synthesisRecords[id]; ok {
			member.SynthesisRevisionMs = persisted.Revision
		}
		members = append(members, member)
		revisionInput = append(revisionInput, id+":"+revision)
		if err := writes.addProcessed(ctx, record, selection.SourceKind, repository, repositoryLocalOnly, handle.Enrichment[id], synthesisRecords[id]); err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
		}
	}
	manifest := sessionbackupv1.Manifest{
		RequiredVersions: []string{sessionbackupv1.ParsedRecordVersion, sessionbackupv1.SchemaVersion},
		Source:           sessionbackupv1.SourceIdentity{Kind: selection.SourceKind, SourceID: selection.SourceID, Agent: vendors.AgentClaude, SourceRevision: hashStrings(revisionInput)},
		Repository:       sessionbackupv1.RepositoryIdentity{Canonical: repository, VCS: "git"},
		Producer:         sessionbackupv1.ProducerIdentity{Name: "coslash", Version: manager.collectorVersion, ParserVersion: manager.parserVersion},
		Family:           sessionbackupv1.FamilyIdentity{FamilyID: selection.SessionID, RootMemberID: selection.SessionID},
		Members:          members, Artifacts: writes.artifacts,
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

func validateClaudeJSON(file string, lines bool) error {
	input, err := os.Open(file)
	if err != nil {
		return err
	}
	defer input.Close()
	decoder := json.NewDecoder(input)
	count := 0
	for {
		var row json.RawMessage
		err := decoder.Decode(&row)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		count++
		if !lines && count > 1 {
			return sessionbackupv1.ErrInvalid
		}
	}
	if count == 0 {
		return sessionbackupv1.ErrInvalid
	}
	if lines {
		if _, err := input.Seek(-1, io.SeekEnd); err != nil {
			return err
		}
		last := make([]byte, 1)
		if _, err := input.Read(last); err != nil || last[0] != '\n' {
			return sessionbackupv1.ErrInvalid
		}
	}
	return nil
}

func sameClaudeFiles(before, after []string) bool {
	sort.Strings(after)
	return reflect.DeepEqual(before, after)
}

func safeClaudeMemberID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 512 || !utf8.ValidString(id) || strings.ContainsAny(id, "/\\") || strings.TrimSpace(id) != id {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func claudeInputWithinProjects(file, root string) bool {
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedFile)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
