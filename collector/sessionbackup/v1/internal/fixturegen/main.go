package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

const (
	rootID  = "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	childID = "019f4dde-db5b-7100-bdc0-09b5aaaac570"
)

type fixtureIndex struct {
	SchemaVersion string         `json:"schemaVersion"`
	Generator     string         `json:"generator"`
	Fixtures      []fixtureEntry `json:"fixtures"`
}

type fixtureEntry struct {
	Path   string `json:"path"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason"`
}

func main() {
	root := filepath.Join("testdata", "fixtures")
	if err := os.RemoveAll(root); err != nil {
		panic(err)
	}
	databaseRoot := filepath.Join("testdata", "database-rows")
	if err := os.RemoveAll(databaseRoot); err != nil {
		panic(err)
	}
	databaseBytes, err := sessionbackupv1.FreezeDatabaseRows(sessionbackupv1.DatabaseRows{
		Database: "future-cache",
		Tables: []sessionbackupv1.DatabaseTable{{
			Name: "records", Columns: []string{"session_id"}, Rows: []sessionbackupv1.DatabaseRow{{
				MemberID: "member-a", Values: []sessionbackupv1.DatabaseValue{{Type: "text", Value: "member-a"}},
			}},
		}},
	})
	if err != nil {
		panic(err)
	}
	write(filepath.Join(databaseRoot, "invalid-cross-session.json"), databaseBytes)
	manifest, blobs := validFixture()
	writeBundle(filepath.Join(root, "valid", "family"), manifest, blobs, "")

	entries := []fixtureEntry{{Path: "valid/family", Valid: true}}
	invalid := []struct {
		name   string
		reason string
		build  func(sessionbackupv1.Manifest, map[string][]byte) (sessionbackupv1.Manifest, map[string][]byte, string)
	}{
		{"missing-artifact", "missing artifact", func(m sessionbackupv1.Manifest, b map[string][]byte) (sessionbackupv1.Manifest, map[string][]byte, string) {
			delete(b, m.Artifacts[0].LogicalName)
			return m, b, m.Artifacts[0].LogicalName
		}},
		{"wrong-size", "wrong artifact size", func(m sessionbackupv1.Manifest, b map[string][]byte) (sessionbackupv1.Manifest, map[string][]byte, string) {
			m.Artifacts[0].ByteLength++
			m.Summary.TotalBytes++
			m.CompleteBackupSHA256 = rehash(m)
			return m, b, ""
		}},
		{"wrong-hash", "wrong artifact hash", func(m sessionbackupv1.Manifest, b map[string][]byte) (sessionbackupv1.Manifest, map[string][]byte, string) {
			m.Artifacts[0].SHA256 = fmt.Sprintf("%064d", 0)
			m.CompleteBackupSHA256 = rehash(m)
			return m, b, ""
		}},
	}
	for _, item := range invalid {
		copyManifest := manifest
		copyManifest.Members = append([]sessionbackupv1.Member(nil), manifest.Members...)
		copyManifest.Artifacts = append([]sessionbackupv1.Artifact(nil), manifest.Artifacts...)
		copyManifest.RequiredVersions = append([]string(nil), manifest.RequiredVersions...)
		copyBlobs := cloneBlobs(blobs)
		candidate, candidateBlobs, omitted := item.build(copyManifest, copyBlobs)
		writeBundle(filepath.Join(root, "invalid", item.name), candidate, candidateBlobs, omitted)
		entries = append(entries, fixtureEntry{Path: filepath.ToSlash(filepath.Join("invalid", item.name)), Reason: item.reason})
	}
	index := fixtureIndex{SchemaVersion: sessionbackupv1.SchemaVersion, Generator: "fixturegen/1", Fixtures: entries}
	data, err := json.Marshal(index)
	if err != nil {
		panic(err)
	}
	write(filepath.Join(root, "index.json"), data)
}

func validFixture() (sessionbackupv1.Manifest, map[string][]byte) {
	seedRecordBytes, err := os.ReadFile(filepath.Join("..", "..", "fullsession", "v1", "testdata", "fixtures", "valid", "codex.json"))
	if err != nil {
		panic(err)
	}
	rootRecord, err := fullsessionv1.Decode(seedRecordBytes)
	if err != nil {
		panic(err)
	}
	rootRecord.Session.WorkingDirectory = "synthetic-workspace"
	rootRecord, err = fullsessionv1.Freeze(rootRecord)
	if err != nil {
		panic(err)
	}
	rootRecordBytes, err := fullsessionv1.Marshal(rootRecord)
	if err != nil {
		panic(err)
	}
	childRecord := rootRecord
	childRecord.SessionID = childID
	childRecord.ParentSessionID = rootID
	childRecord.Session.Branch = nil
	childRecord.Session.FileEdits = nil
	childRecord.Session.EditedFileCount = 0
	childRecord.Session.Synthesis = nil
	childRecord.Session.SynthesisPending = false
	childRecord, err = fullsessionv1.Freeze(childRecord)
	if err != nil {
		panic(err)
	}
	childRecordBytes, err := fullsessionv1.Marshal(childRecord)
	if err != nil {
		panic(err)
	}
	repository := "github.com/example/synthetic"
	fallbackBranch := "filesystem-fallback"
	rootLastEdit := int64(1789948801000)
	childLastEdit := int64(1789948802000)
	rootEnrichment, err := sessionbackupv1.MarshalEnrichment(sessionbackupv1.Enrichment{
		Repository: &repository, RepositoryLocalOnly: false,
		Git: &sessionbackupv1.GitDrift{BaseBranch: "main", Ahead: 1, Behind: 0}, LastEditAtMs: &rootLastEdit,
	})
	if err != nil {
		panic(err)
	}
	childEnrichment, err := sessionbackupv1.MarshalEnrichment(sessionbackupv1.Enrichment{
		Repository: &repository, RepositoryLocalOnly: true, FilesystemFallbackBranch: &fallbackBranch, LastEditAtMs: &childLastEdit,
	})
	if err != nil {
		panic(err)
	}
	model := "fixture-model"
	if rootRecord.Session.Model != nil {
		model = *rootRecord.Session.Model
	}
	rootSynthesis, err := sessionbackupv1.MarshalSynthesisRecord(sessionbackupv1.SynthesisRecord{
		Agent: "codex", SessionID: rootID, Revision: rootRecord.Session.LastActivityAtMs,
		Model: model, GeneratedAt: 1789948803000, Synthesis: *rootRecord.Session.Synthesis,
	})
	if err != nil {
		panic(err)
	}
	blobs := map[string][]byte{
		"members/root/raw/rollout.jsonl":                   []byte("{\"timestamp\":\"2026-09-22T00:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"" + rootID + "\"}}\n{\"timestamp\":\"2026-09-22T00:00:01Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"user_message\",\"message\":\"synthetic prompt\"}}\n"),
		"members/root/raw/session-index-row.jsonl":         []byte("{\"id\":\"" + rootID + "\",\"thread_name\":\"Synthetic contract fixture\"}\n"),
		"members/root/processed/full-session-record.json":  rootRecordBytes,
		"members/root/processed/change-000000.patch":       []byte(rootRecord.Session.FileEdits[0].Changes[0].Text),
		"members/root/processed/change-000001.txt":         []byte(rootRecord.Session.FileEdits[0].Changes[1].Text),
		"members/root/processed/enrichment.json":           rootEnrichment,
		"members/root/processed/synthesis.json":            rootSynthesis,
		"members/child/raw/rollout.jsonl":                  []byte("{\"timestamp\":\"2026-09-22T00:00:02Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"" + childID + "\",\"parent_thread_id\":\"" + rootID + "\"}}\n"),
		"members/child/processed/full-session-record.json": childRecordBytes,
		"members/child/processed/enrichment.json":          childEnrichment,
	}
	artifacts := []sessionbackupv1.Artifact{
		artifact("members/root/raw/rollout.jsonl", rootID, sessionbackupv1.ArtifactSourceCodex, sessionbackupv1.KindRawTranscript, "rollout", "application/x-ndjson"),
		artifact("members/root/raw/session-index-row.jsonl", rootID, sessionbackupv1.ArtifactSourceCodex, sessionbackupv1.KindRawSidecar, "session_index", "application/x-ndjson"),
		artifact("members/root/processed/full-session-record.json", rootID, sessionbackupv1.ArtifactSourceCoSlash, sessionbackupv1.KindParsedSessionRecord, rootRecord.RevisionID, "application/json"),
		artifact("members/root/processed/change-000000.patch", rootID, sessionbackupv1.ArtifactSourceCoSlash, sessionbackupv1.KindExactChangeBody, rootRecord.Session.FileEdits[0].Changes[0].ID, "text/x-diff; charset=utf-8"),
		artifact("members/root/processed/change-000001.txt", rootID, sessionbackupv1.ArtifactSourceCoSlash, sessionbackupv1.KindExactChangeBody, rootRecord.Session.FileEdits[0].Changes[1].ID, "text/plain; charset=utf-8"),
		artifact("members/root/processed/enrichment.json", rootID, sessionbackupv1.ArtifactSourceCoSlash, sessionbackupv1.KindSessionEnrichment, "session-overlay", "application/json"),
		artifact("members/root/processed/synthesis.json", rootID, sessionbackupv1.ArtifactSourceCoSlash, sessionbackupv1.KindSynthesis, "persisted-synthesis", "application/json"),
		artifact("members/child/raw/rollout.jsonl", childID, sessionbackupv1.ArtifactSourceCodex, sessionbackupv1.KindRawTranscript, "rollout", "application/x-ndjson"),
		artifact("members/child/processed/full-session-record.json", childID, sessionbackupv1.ArtifactSourceCoSlash, sessionbackupv1.KindParsedSessionRecord, childRecord.RevisionID, "application/json"),
		artifact("members/child/processed/enrichment.json", childID, sessionbackupv1.ArtifactSourceCoSlash, sessionbackupv1.KindSessionEnrichment, "session-overlay", "application/json"),
	}
	manifest := sessionbackupv1.Manifest{
		RequiredVersions: []string{sessionbackupv1.SchemaVersion, sessionbackupv1.ParsedRecordVersion},
		Source:           sessionbackupv1.SourceIdentity{Kind: sessionbackupv1.SourceLocal, SourceID: rootRecord.SourceID, Agent: "codex", SourceRevision: "source-revision-001"},
		Repository:       sessionbackupv1.RepositoryIdentity{Canonical: "github.com/example/synthetic", VCS: "git"},
		Producer:         sessionbackupv1.ProducerIdentity{Name: "coslash", Version: "fixturegen-1", ParserVersion: "codex-parser-1"},
		Family:           sessionbackupv1.FamilyIdentity{FamilyID: rootID, RootMemberID: rootID},
		Members: []sessionbackupv1.Member{
			{MemberID: childID, ParentMemberID: rootID, SourceRevision: "child-source-revision-001"},
			{MemberID: rootID, SourceRevision: "root-source-revision-001", SynthesisRevisionMs: rootRecord.Session.LastActivityAtMs},
		},
		Artifacts: artifacts,
	}
	frozen, err := sessionbackupv1.Freeze(manifest, blobs)
	if err != nil {
		panic(err)
	}
	return frozen, blobs
}

func artifact(name, member, source, kind, sourceKey, mediaType string) sessionbackupv1.Artifact {
	return sessionbackupv1.Artifact{LogicalName: name, MemberID: member, Source: source, Kind: kind, SourceKey: sourceKey, MediaType: mediaType, Encoding: sessionbackupv1.EncodingIdentity}
}

func rehash(manifest sessionbackupv1.Manifest) string {
	manifest.CompleteBackupSHA256 = ""
	data, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeBundle(root string, manifest sessionbackupv1.Manifest, blobs map[string][]byte, omitted string) {
	data, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	write(filepath.Join(root, sessionbackupv1.ManifestFileName), data)
	for name, blob := range blobs {
		if name != omitted {
			write(filepath.Join(root, filepath.FromSlash(name)), blob)
		}
	}
}

func write(name string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		panic(err)
	}
}

func cloneBlobs(blobs map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(blobs))
	for name, blob := range blobs {
		result[name] = append([]byte(nil), blob...)
	}
	return result
}
