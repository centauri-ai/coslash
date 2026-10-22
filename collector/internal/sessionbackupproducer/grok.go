package sessionbackupproducer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/grok"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

type grokInput struct {
	path, memberID, relative string
	directory                bool
}

func (manager *Manager) captureGrok(ctx context.Context, staging string, selection Selection, handle SourceHandle) (*Prepared, error) {
	if handle.Source != vendors.LocalReadSource {
		return nil, captureFailure(sessionbackupv1.ProblemUnsupported, "", false)
	}
	root, err := grok.Root()
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnavailable, "", true)
	}
	family, err := grokCaptureFamily(ctx, root, selection.SessionID)
	if err != nil {
		return nil, err
	}
	inputs, ids, err := grokFamilyInputs(ctx, root, family)
	if err != nil {
		return nil, err
	}
	if len(inputs) > sessionbackupv1.MaxArtifacts-2*len(ids) {
		return nil, captureTooLarge("")
	}
	selection.SessionID = family.ID
	paths := []string{}
	for _, input := range inputs {
		if !input.directory {
			paths = append(paths, input.path)
		}
	}
	before, err := vendors.FingerprintSourceFilesFreshContext(ctx, handle.Source, root, paths)
	if err != nil {
		return nil, captureFailure(sessionbackupv1.ProblemUnreadable, "", true)
	}
	if !withinKnownBounds(before) {
		return nil, captureTooLarge("")
	}
	writes := &artifactWriter{root: staging, ctx: ctx, evidence: map[string]sessionbackupv1.ArtifactEvidence{}}
	frozenDirs := make([]string, len(family.Dirs))
	hashes := map[string][]string{}
	proofs := []string{}
	for i, id := range ids {
		frozenDirs[i] = filepath.Join(staging, "members", id, "raw")
	}
	for _, input := range inputs {
		name := "members/" + input.memberID + "/raw/" + filepath.ToSlash(input.relative)
		if input.directory {
			if err := os.MkdirAll(filepath.Join(staging, filepath.FromSlash(name)), 0o700); err != nil {
				return nil, captureFailure(sessionbackupv1.ProblemUnavailable, "", true)
			}
			hashes[input.memberID] = append(hashes[input.memberID], "directory:"+filepath.ToSlash(input.relative))
			continue
		}
		kind, media := sessionbackupv1.KindRawSidecar, "application/json"
		if input.relative == "updates.jsonl" {
			kind, media = sessionbackupv1.KindRawTranscript, "application/x-ndjson"
		}
		if input.relative == "plan.md" {
			media = "text/markdown; charset=utf-8"
		}
		artifact := sessionbackupv1.Artifact{LogicalName: name, MemberID: input.memberID, Source: sessionbackupv1.ArtifactSourceGrok, Kind: kind, SourceKey: filepath.ToSlash(input.relative), MediaType: media, Encoding: sessionbackupv1.EncodingIdentity}
		if err := writes.stream(ctx, artifact, func() (io.ReadCloser, error) { return handle.Source.Open(input.path) }); err != nil {
			return nil, sourceReadFailure(err, kind)
		}
		if input.relative != "updates.jsonl" && writes.evidence[name].ByteLength > 64<<20 {
			return nil, captureTooLarge(kind)
		}
		hashes[input.memberID] = append(hashes[input.memberID], filepath.ToSlash(input.relative)+":"+writes.evidence[name].SHA256)
		if strings.HasPrefix(filepath.ToSlash(input.relative), "non-children/") {
			proofs = append(proofs, filepath.Join(staging, filepath.FromSlash(name)))
		}
	}
	if manager.afterRawCopy != nil {
		manager.afterRawCopy()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Size and mtime alone miss same-size rewrites with restored timestamps.
	for _, input := range inputs {
		if input.directory {
			continue
		}
		file, err := handle.Source.Open(input.path)
		if err != nil {
			return nil, captureFailure(sessionbackupv1.ProblemUnstable, "", true)
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: file}, sessionbackupv1.MaxArtifactBytes+1))
		closeErr := file.Close()
		evidence := writes.evidence["members/"+input.memberID+"/raw/"+filepath.ToSlash(input.relative)]
		if readErr != nil || closeErr != nil || n != evidence.ByteLength || hex.EncodeToString(hash.Sum(nil)) != evidence.SHA256 {
			return nil, captureFailure(sessionbackupv1.ProblemUnstable, "", true)
		}
	}
	after, err := vendors.FingerprintSourceFilesFreshContext(ctx, handle.Source, root, paths)
	if err != nil || !reflect.DeepEqual(before, after) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, "", true)
	}
	current, err := grokCaptureFamily(ctx, root, selection.SessionID)
	if err != nil || !reflect.DeepEqual(family, current) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, "", true)
	}
	currentInputs, currentIDs, err := grokFamilyInputs(ctx, root, current)
	if err != nil || !reflect.DeepEqual(inputs, currentInputs) || !reflect.DeepEqual(ids, currentIDs) {
		return nil, captureFailure(sessionbackupv1.ProblemUnstable, "", true)
	}
	parsed, err := grok.ParseFrozenFamilyContext(ctx, family, frozenDirs, proofs)
	if err != nil {
		return nil, processedCaptureFailure(err, sessionbackupv1.KindParsedSessionRecord)
	}
	if len(parsed) != len(ids) {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
	}
	for i, item := range parsed {
		if item.Session.ID != ids[i] {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
		}
	}
	records, err := fullsessionrecord.FromParsedFamilyContext(ctx, selection.SourceID, vendors.AgentGrok, vendors.LocalReadSource, parsed, vendors.EmptySessionMetadata())
	if err != nil {
		return nil, processedCaptureFailure(err, sessionbackupv1.KindParsedSessionRecord)
	}
	if len(records) != len(ids) {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
	}
	if len(records) == 0 || records[0].SessionID != selection.SessionID || records[0].ParentSessionID != "" {
		return nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
	}
	repository, localOnly := repositoryIdentity(ctx, selection.SourceKind, records[0].Session.WorkingDirectory, handle.Enrichment[selection.SessionID])
	if repository == "" {
		return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSessionEnrichment, false)
	}
	members := make([]sessionbackupv1.Member, 0, len(records))
	revisions := []string{}
	for i, record := range records {
		if record.SessionID != ids[i] {
			return nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
		}
		revision := hashStrings(hashes[record.SessionID])
		member := sessionbackupv1.Member{MemberID: record.SessionID, ParentMemberID: record.ParentSessionID, SourceRevision: revision}
		persisted := sessionbackupv1.SynthesisRecord{}
		if manager.synthesis != nil {
			cached, loadErr := manager.synthesis.LoadRecord(vendors.AgentGrok, record.SessionID)
			if loadErr != nil && !errors.Is(loadErr, fs.ErrNotExist) {
				return nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindSynthesis, false)
			}
			if loadErr == nil && cached.Revision == record.Session.LastActivityAtMs {
				persisted = sessionbackupv1.SynthesisRecord{Agent: vendors.AgentGrok, SessionID: record.SessionID, Revision: cached.Revision, Model: cached.Model, GeneratedAt: cached.GeneratedAt, Synthesis: fullsessionv1.SessionSynthesis{Goals: append([]string(nil), cached.Synthesis.Goals...), Outcome: cached.Synthesis.Outcome, KeyDecisions: append([]string(nil), cached.Synthesis.KeyDecisions...), NextStep: cached.Synthesis.NextStep}}
				member.SynthesisRevisionMs = cached.Revision
			}
		}
		members = append(members, member)
		revisions = append(revisions, record.SessionID+":"+revision)
		if err := writes.addProcessed(ctx, record, handle.Home, selection.SourceKind, repository, localOnly, handle.Enrichment[record.SessionID], persisted); err != nil {
			return nil, processedCaptureFailure(err, "")
		}
	}
	manifest := sessionbackupv1.Manifest{
		RequiredVersions: []string{sessionbackupv1.ParsedRecordVersion, sessionbackupv1.SchemaVersion},
		Source:           sessionbackupv1.SourceIdentity{Kind: selection.SourceKind, SourceID: selection.SourceID, Agent: vendors.AgentGrok, SourceRevision: hashStrings(revisions)},
		Repository:       sessionbackupv1.RepositoryIdentity{Canonical: repository, VCS: "git"},
		Producer:         sessionbackupv1.ProducerIdentity{Name: "coslash", Version: manager.collectorVersion, ParserVersion: manager.parserVersion},
		Family:           sessionbackupv1.FamilyIdentity{FamilyID: selection.SessionID, RootMemberID: selection.SessionID}, Members: members, Artifacts: writes.artifacts,
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
	return &Prepared{BundleID: frozen.CompleteBackupSHA256, Selection: selection, Manifest: frozen, Coverage: coverage(frozen)}, nil
}

func grokCaptureFamilies(ctx context.Context, root string) ([]grok.FamilyRef, map[string]bool, error) {
	groups, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
	}
	dirs := []string{}
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !group.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, group.Name()))
		if err != nil {
			return nil, nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, group.Name(), entry.Name())
			for _, name := range []string{"summary.json", "updates.jsonl"} {
				path := filepath.Join(dir, name)
				if _, err := os.Lstat(path); err == nil {
					if !claudeInputWithinProjects(path, root) {
						return nil, nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
					}
				}
			}
			subagents := filepath.Join(dir, "subagents")
			if info, err := os.Lstat(subagents); err == nil {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return nil, nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
				}
				spawns, err := os.ReadDir(subagents)
				if err != nil {
					return nil, nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
				}
				for _, spawn := range spawns {
					if !spawn.IsDir() {
						continue
					}
					path := filepath.Join(subagents, spawn.Name(), "meta.json")
					if _, err := os.Lstat(path); err == nil && !claudeInputWithinProjects(path, root) {
						return nil, nil, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
					}
				}
			}
			dirs = append(dirs, dir)
		}
	}
	families, oversizedMembers, err := grok.PlanFamiliesContextWithTranscriptLimit(ctx, dirs, sessionbackupv1.MaxArtifactBytes)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	// An incomplete scan can still return independently verified families.
	if err != nil && len(families) == 0 {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, oversizedMembers, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
	}
	return families, oversizedMembers, err
}

func grokCaptureFamily(ctx context.Context, root, id string) (grok.FamilyRef, error) {
	failure := func() (grok.FamilyRef, error) {
		return grok.FamilyRef{}, captureFailure(sessionbackupv1.ProblemUnattributable, "", false)
	}
	families, oversizedMembers, err := grokCaptureFamilies(ctx, root)
	if oversizedMembers[id] {
		return grok.FamilyRef{}, captureTooLarge(sessionbackupv1.KindRawTranscript)
	}
	if err != nil && len(families) == 0 {
		return grok.FamilyRef{}, err
	}
	for _, family := range families {
		for _, dir := range family.Dirs {
			member, err := grokCaptureID(dir)
			if err != nil {
				return failure()
			}
			if member == id {
				return family, nil
			}
		}
	}
	return failure()
}

func grokCaptureID(dir string) (string, error) {
	file, err := os.Open(filepath.Join(dir, "summary.json"))
	if err != nil {
		return "", err
	}
	defer file.Close()
	var summary struct {
		Info struct {
			ID string `json:"id"`
		} `json:"info"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 64<<20)).Decode(&summary); err != nil {
		return "", err
	}
	if !safeClaudeMemberID(summary.Info.ID) {
		return "", sessionbackupv1.ErrInvalid
	}
	return summary.Info.ID, nil
}

func grokCaptureInputs(ctx context.Context, root string, dirs []string) ([]grokInput, []string, error) {
	inputs := []grokInput{}
	ids := []string{}
	for _, dir := range dirs {
		id, err := grokCaptureID(dir)
		if err != nil {
			return nil, nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
		}
		ids = append(ids, id)
		present := map[string]bool{}
		err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			if relative == "." {
				return nil
			}
			rel := filepath.ToSlash(relative)
			if entry.IsDir() {
				parts := strings.Split(rel, "/")
				if rel != "goal" && rel != "subagents" && rel != "compaction_checkpoints" && !(len(parts) == 2 && parts[0] == "subagents") {
					return filepath.SkipDir
				}
				if !grokDirectoryWithin(path, root) {
					return sessionbackupv1.ErrInvalid
				}
				if len(inputs) >= sessionbackupv1.MaxArtifacts {
					return errArtifactTooLarge
				}
				inputs = append(inputs, grokInput{path, id, relative, true})
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 && (rel == "goal" || rel == "subagents" || rel == "compaction_checkpoints" || strings.HasPrefix(rel, "subagents/")) {
				return sessionbackupv1.ErrInvalid
			}
			if !grok.IsSourceFile(relative) {
				return nil
			}
			if !claudeInputWithinProjects(path, root) {
				return sessionbackupv1.ErrInvalid
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() < 0 || info.Size() > sessionbackupv1.MaxArtifactBytes {
				return errArtifactTooLarge
			}
			if rel != "updates.jsonl" && info.Size() > 64<<20 {
				return errArtifactTooLarge
			}
			inputs = append(inputs, grokInput{path, id, relative, false})
			present[rel] = true
			if len(inputs) > sessionbackupv1.MaxArtifacts {
				return errArtifactTooLarge
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errArtifactTooLarge) {
				return nil, nil, captureTooLarge("")
			}
			if errors.Is(err, sessionbackupv1.ErrInvalid) {
				return nil, nil, captureFailure(sessionbackupv1.ProblemInvalid, "", false)
			}
			return nil, nil, sourceReadFailure(err, "")
		}
		if !present["summary.json"] || !present["updates.jsonl"] {
			return nil, nil, captureFailure(sessionbackupv1.ProblemUnavailable, "", true)
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].path < inputs[j].path })
	return inputs, ids, nil
}

func grokDirectoryWithin(path, root string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func grokFamilyInputs(ctx context.Context, root string, family grok.FamilyRef) ([]grokInput, []string, error) {
	if len(family.Dirs) > sessionbackupv1.MaxMembers {
		return nil, nil, captureTooLarge("")
	}
	inputs, ids, err := grokCaptureInputs(ctx, root, family.Dirs)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range family.NonChildSummaryPaths() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		id, err := grokCaptureID(filepath.Dir(path))
		if err != nil || !claudeInputWithinProjects(path, root) {
			return nil, nil, captureFailure(sessionbackupv1.ProblemUnattributable, sessionbackupv1.KindRawSidecar, false)
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() > 64<<20 {
			return nil, nil, captureFailure(sessionbackupv1.ProblemInvalid, sessionbackupv1.KindRawSidecar, false)
		}
		inputs = append(inputs, grokInput{path, family.ID, filepath.Join("non-children", id, "summary.json"), false})
	}
	if len(inputs) > sessionbackupv1.MaxArtifacts {
		return nil, nil, captureTooLarge("")
	}
	return inputs, ids, nil
}

// LocalGrokSourceRevisions hashes the existing producer inputs once per queue
// discovery pass. Root detail revisions do not cover every descendant's usage.
// Revisions and ordinary failures are keyed by session ID; the final error aborts.
func LocalGrokSourceRevisions(ctx context.Context) (map[string]string, map[string]error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	root, err := grok.Root()
	if err != nil {
		return nil, nil, err
	}
	families, oversizedMembers, err := grokCaptureFamilies(ctx, root)
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil && len(families) == 0 && len(oversizedMembers) == 0 {
		var failure *captureError
		if errors.As(err, &failure) {
			_, err = captureProblem(Selection{SourceKind: sessionbackupv1.SourceLocal, Agent: vendors.AgentGrok}, failure)
		}
		return nil, nil, err
	}
	revisions := make(map[string]string, len(families))
	failures := map[string]error{}
	for id := range oversizedMembers {
		_, failures[id] = captureProblem(Selection{SourceKind: sessionbackupv1.SourceLocal, Agent: vendors.AgentGrok, SessionID: id}, &captureError{code: sessionbackupv1.ProblemInvalid, kind: sessionbackupv1.KindRawTranscript, tooLarge: true})
	}
	for _, family := range families {
		revision, err := GrokSourceRevision(ctx, family)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, nil, err
			}
			failures[family.ID] = err
			continue
		}
		revisions[family.ID] = revision
	}
	return revisions, failures, nil
}

// GrokSourceRevision uses the unchanged verified planning context, including
// independent fork proof. It reads only this family's attributable inputs.
func GrokSourceRevision(ctx context.Context, family grok.FamilyRef) (revision string, err error) {
	defer func() {
		var failure *captureError
		if errors.As(err, &failure) {
			_, err = captureProblem(Selection{SourceKind: sessionbackupv1.SourceLocal, Agent: vendors.AgentGrok, SessionID: family.ID}, failure)
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := grok.Root()
	if err != nil {
		return "", err
	}

	inputs, _, err := grokFamilyInputs(ctx, root, family)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}

	parts := make([]string, 0, len(inputs))
	var total int64
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		identity := input.memberID + ":" + filepath.ToSlash(input.relative)
		if input.directory {
			parts = append(parts, identity+":directory")
			continue
		}
		file, err := os.Open(input.path)
		if err != nil {
			return "", sourceReadFailure(err, "")
		}
		hash := sha256.New()
		maximum := min(sessionbackupv1.MaxArtifactBytes, sessionbackupv1.MaxTotalBytes-total)
		n, readErr := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: file}, maximum+1))
		closeErr := file.Close()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if readErr != nil || closeErr != nil {
			return "", sourceReadFailure(errors.Join(readErr, closeErr), "")
		}
		if n > maximum {
			return "", captureTooLarge("")
		}
		total += n
		parts = append(parts, identity+":"+hex.EncodeToString(hash.Sum(nil)))
	}
	return hashStrings(parts), nil
}
