package cursor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestCursorEnrichmentPreservesMergedEditsDuringSharedFinalization(t *testing.T) {
	parsed := &vendors.ParsedSession{Session: &session.Session{
		ID: "session", EditedFileCount: 1,
		SessionDetails: session.SessionDetails{FileEdits: []session.FileEdit{{Path: "a.go", Additions: 1}}},
	}}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("session").FileEdits = []session.FileEdit{{Path: "b.go", Additions: 2}}

	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, metadata)
	vendors.ApplySessionEnrichment(parsed, metadata.Lookup("session"))

	if len(parsed.Session.FileEdits) != 2 || parsed.Session.FileEdits[0].Path != "a.go" || parsed.Session.FileEdits[1].Path != "b.go" {
		t.Fatalf("file edits = %#v, want merged transcript and checkpoint edits", parsed.Session.FileEdits)
	}
}

func TestCursorEnrichmentAppliesSharedFactsMetadataAndCost(t *testing.T) {
	parsed := &vendors.ParsedSession{Session: &session.Session{ID: "session", Tokens: map[string]session.ModelTokens{}}}
	metadata := vendors.EmptySessionMetadata()
	entry := metadata.Session("session")
	entry.Model = "composer-2.5-fast"
	entry.Entrypoint = entrypointIDE
	entry.PullRequests = 2
	contextTokens, contextWindow, recordedCost := 100_001, 200_000, 1.25
	entry.Usage = vendors.SessionUsage{
		Tokens:        map[string]session.ModelTokens{"composer-2.5-fast": {InputTokens: 10}},
		ContextTokens: &contextTokens, ContextWindow: &contextWindow, RecordedCost: &recordedCost,
	}

	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, metadata)

	if parsed.Session.Model == nil || *parsed.Session.Model != "composer-2.5-fast" ||
		parsed.Session.Entrypoint == nil || *parsed.Session.Entrypoint != entrypointIDE ||
		parsed.Session.ContextTokens == nil || *parsed.Session.ContextTokens != contextTokens ||
		parsed.Session.ContextWindow == nil || *parsed.Session.ContextWindow != contextWindow ||
		parsed.Session.PullRequests != 2 || parsed.Session.Cost == nil || *parsed.Session.Cost != recordedCost {
		t.Fatalf("session missing shared metadata or cost: %#v", parsed.Session)
	}
}

func TestCursorEnrichmentRecomputesDurationFromMetadataTimes(t *testing.T) {
	duration := 400
	parsed := &vendors.ParsedSession{Session: &session.Session{
		ID: "session", StartedAt: 100, LastActivityTime: 500, DurationMs: &duration,
	}}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("session").StartedAt = 200
	metadata.Session("session").LastActivityAt = 300

	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, metadata)

	if parsed.Session.DurationMs == nil || *parsed.Session.DurationMs != 100 {
		t.Fatalf("duration = %v, want 100", parsed.Session.DurationMs)
	}
}

func TestGetSessionFactsAppliesMetadataRelationship(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	parentID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	path := filepath.Join(home, ".cursor", "projects", "repo", "agent-transcripts", childID, childID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"role":"user","message":{"content":[{"type":"text","text":"child prompt"}]}}` + "\n" +
		`{"type":"turn_ended","status":"success"}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(cursorGlobalStorage(home), "state.vscdb")
	if err := createMetadataTestDB(statePath); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	header := `{"subagentInfo":{"parentComposerId":"` + parentID + `","toolCallId":"call-1"}}`
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, value) VALUES (?, ?)`, childID, header); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	parsed, err := GetSessionFacts(childID)
	if err != nil {
		t.Fatal(err)
	}
	if parsed == nil || parsed.ParentID != parentID {
		t.Fatalf("facts = %#v, want metadata-linked child of %s", parsed, parentID)
	}
}

func TestCursorEnrichmentClearsStaleLivenessForStoppedSession(t *testing.T) {
	parsed := &vendors.ParsedSession{Session: &session.Session{ID: "stopped"}, Stopped: true}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("stopped").Live = "interactive"

	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, metadata)

	if got := metadata.Lookup("stopped").Live; got != "" {
		t.Fatalf("live = %q, want empty after terminal stop", got)
	}
}

func TestCursorEnrichmentAppliesIdleLiveStatusForFacts(t *testing.T) {
	parsed := &vendors.ParsedSession{Session: &session.Session{ID: "live"}}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("live").Live = "interactive"

	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, metadata)

	if parsed.Session.Status == nil || *parsed.Session.Status != "idle" {
		t.Fatalf("status = %v, want idle", parsed.Session.Status)
	}
}

func TestCursorEnrichmentDoesNotFinalizeInteractiveTrailingReply(t *testing.T) {
	const id = "01234567-89ab-4def-8123-456789abcdef"
	path := filepath.Join(t.TempDir(), "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"role":"user","message":{"content":[{"type":"text","text":"prompt"}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"text","text":"reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(id).Live = "interactive"

	applyCursorEnrichment([]*vendors.ParsedSession{parsed}, metadata)

	for _, entry := range parsed.Session.Digest {
		if entry.Category == session.DigestRecap {
			t.Fatalf("unexpected recap for interactive trailing reply: %#v", entry)
		}
	}
}

func TestSelectCursorFilesPreservesLiveFamily(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(tempFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	rootID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	files := []string{
		filepath.Join("agent-transcripts", rootID, rootID+".jsonl"),
		filepath.Join("agent-transcripts", rootID, "subagents", childID+".jsonl"),
	}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(childID).Live = "interactive"

	got := selectCursorFilesSourceWithMetadata(
		statReadSource{info: info}, files, info.ModTime().UnixMilli()+1, metadata,
	)

	if len(got) != 2 {
		t.Fatalf("selected files = %v, want live family", got)
	}
}

func TestCursorFamilyFilesSelectsOnlyRequestedFamily(t *testing.T) {
	rootID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	otherID := "00000000-0000-4000-8000-000000000003"
	root := filepath.Join("projects", "repo", "agent-transcripts", rootID, rootID+".jsonl")
	child := filepath.Join("projects", "repo", "agent-transcripts", rootID, "subagents", childID+".jsonl")
	other := filepath.Join("projects", "repo", "agent-transcripts", otherID, otherID+".jsonl")
	files := []string{root, child, other}

	for _, id := range []string{rootID, childID} {
		got := cursorFamilyFiles(files, id)
		if len(got) != 2 || got[0] != root || got[1] != child {
			t.Fatalf("cursorFamilyFiles(%q) = %v, want [%s %s]", id, got, root, child)
		}
	}
}

func TestCursorFamilyFilesUsesMetadataRelationships(t *testing.T) {
	rootID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	otherID := "00000000-0000-4000-8000-000000000003"
	root := filepath.Join("agent-transcripts", rootID, rootID+".jsonl")
	child := filepath.Join("agent-transcripts", childID, childID+".jsonl")
	other := filepath.Join("agent-transcripts", otherID, otherID+".jsonl")
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(childID).Relationship = vendors.SessionRelationship{ParentID: rootID}

	for _, id := range []string{rootID, childID} {
		got := cursorFamilyFilesWithMetadata([]string{root, child, other}, id, metadata)
		if len(got) != 2 || got[0] != root || got[1] != child {
			t.Fatalf("cursorFamilyFilesWithMetadata(%q) = %v, want [%s %s]", id, got, root, child)
		}
	}
}

func TestApplyRelationshipsClaimsDuplicateTaskDigestsOnce(t *testing.T) {
	parent := &vendors.ParsedSession{
		Session: &session.Session{ID: "parent", SessionDetails: session.SessionDetails{Digest: []session.DigestEntry{
			{Category: session.DigestSubagent, Description: "same task", SpawnKey: "transcript-1"},
			{Category: session.DigestSubagent, Description: "same task", SpawnKey: "transcript-2"},
		}}},
		Spawns: map[string]vendors.SpawnState{
			"transcript-1": {Task: "same task"},
			"transcript-2": {Task: "same task"},
		},
	}
	child1 := &vendors.ParsedSession{Session: &session.Session{ID: "child-1"}}
	child2 := &vendors.ParsedSession{Session: &session.Session{ID: "child-2"}}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("child-1").Relationship = vendors.SessionRelationship{ParentID: "parent", SpawnKey: "side-1", Task: "same task", Active: true}
	metadata.Session("child-2").Relationship = vendors.SessionRelationship{ParentID: "parent", SpawnKey: "side-2", Task: "same task", Completed: true}

	applyRelationships([]*vendors.ParsedSession{parent, child1, child2}, metadata)

	if len(parent.Spawns) != 2 || parent.Spawns["side-1"].Active != true || parent.Spawns["side-2"].Completed != true {
		t.Fatalf("spawns = %#v, want both relationship states", parent.Spawns)
	}
	got := []string{parent.Session.Digest[0].SpawnKey, parent.Session.Digest[1].SpawnKey}
	if !(got[0] == "side-1" && got[1] == "side-2" || got[0] == "side-2" && got[1] == "side-1") {
		t.Fatalf("digest spawn keys = %v, want each relationship exactly once", got)
	}
}

func TestSelectCursorFilesDoesNotCapAllHistory(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(tempFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, vendors.MaxCandidateFilesPerAgent+1)
	for i := range files {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", i)
		files[i] = filepath.Join("agent-transcripts", id, id+".jsonl")
	}

	got := selectCursorFilesSource(statReadSource{info: info}, files, 0)
	if len(got) != len(files) {
		t.Fatalf("selected %d files, want all %d", len(got), len(files))
	}
}

func TestSelectCursorFilesUsesSideStoreActivity(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	path := filepath.Join("agent-transcripts", id, id+".jsonl")
	tempFile := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(tempFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	since := info.ModTime().UnixMilli() + 1
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(id).LastActivityAt = since

	got := selectCursorFilesSourceWithMetadata(statReadSource{info: info}, []string{path}, since, metadata)
	if len(got) != 1 || got[0] != path {
		t.Fatalf("selected files = %v, want side-store-recent session", got)
	}
}

func TestSelectCursorFilesOrdersFamiliesBySideStoreActivity(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(tempFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, vendors.MaxCandidateFilesPerAgent+1)
	for i := range files {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", i)
		files[i] = filepath.Join("agent-transcripts", id, id+".jsonl")
	}
	newestID := IDFromPath(files[len(files)-1])
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(newestID).LastActivityAt = info.ModTime().UnixMilli() + 1

	got := selectCursorFilesSourceWithMetadata(statReadSource{info: info}, files, 1, metadata)
	for _, path := range got {
		if path == files[len(files)-1] {
			return
		}
	}
	t.Fatalf("side-store-newest session %q was omitted from %d selected files", newestID, len(got))
}

func TestSelectCursorFilesPrioritizesLiveFamiliesAtCandidateLimit(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(tempFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, vendors.MaxCandidateFilesPerAgent+1)
	metadata := vendors.EmptySessionMetadata()
	for i := range files {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", i)
		files[i] = filepath.Join("agent-transcripts", id, id+".jsonl")
		metadata.Session(id).LastActivityAt = int64(i + 1)
	}
	liveID := IDFromPath(files[0])
	metadata.Session(liveID).Live = "interactive"

	got := selectCursorFilesSourceWithMetadata(statReadSource{info: info}, files, 1, metadata)
	for _, path := range got {
		if path == files[0] {
			return
		}
	}
	t.Fatalf("live session %q was omitted from %d selected files", liveID, len(got))
}

func TestSelectCursorFilesDoesNotCreateMetadataForRejectedHistory(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(tempFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-000000000001"
	metadata := vendors.EmptySessionMetadata()
	got := selectCursorFilesSourceWithMetadata(
		statReadSource{info: info},
		[]string{filepath.Join("agent-transcripts", id, id+".jsonl")},
		info.ModTime().UnixMilli()+1,
		metadata,
	)

	if len(got) != 0 {
		t.Fatalf("selected files = %v, want none", got)
	}
	if len(metadata.Sessions) != 0 {
		t.Fatalf("metadata entries = %d, want none", len(metadata.Sessions))
	}
}
func TestCursorSourceHealthCountsOnlyRootTranscripts(t *testing.T) {
	rootID := "00000000-0000-4000-8000-000000000001"
	childID := "00000000-0000-4000-8000-000000000002"
	standaloneChildID := "00000000-0000-4000-8000-000000000003"
	scan := &vendors.SourceScan{Files: []string{
		filepath.Join("agent-transcripts", rootID, rootID+".jsonl"),
		filepath.Join("agent-transcripts", rootID, "subagents", childID+".jsonl"),
		filepath.Join("agent-transcripts", standaloneChildID, standaloneChildID+".jsonl"),
	}}
	metadata := vendors.EmptySessionMetadata()
	metadata.Session(standaloneChildID).Relationship = vendors.SessionRelationship{ParentID: rootID}

	health := cursorSourceHealth("/cursor", scan, metadata)
	if health.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1", health.Sessions)
	}
}

func TestCursorSourceHealthDeduplicatesRootFragments(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	scan := &vendors.SourceScan{Files: []string{
		filepath.Join("projects", "one", "agent-transcripts", id, id+".jsonl"),
		filepath.Join("projects", "two", "agent-transcripts", id, id+".jsonl"),
	}}

	health := cursorSourceHealth("/cursor", scan, vendors.EmptySessionMetadata())
	if health.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1", health.Sessions)
	}
}

func TestSelectFamilyRejectsParentCycle(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	parsed := []*vendors.ParsedSession{{
		Session:  &session.Session{ID: id},
		ParentID: id,
	}}
	done := make(chan []*vendors.ParsedSession, 1)
	go func() { done <- selectFamily(parsed, id) }()

	select {
	case family := <-done:
		if family != nil {
			t.Fatalf("family = %v, want cycle rejected", family)
		}
	case <-time.After(time.Second):
		t.Fatal("selectFamily did not terminate for a self-parent cycle")
	}
}

func TestCursorPathIDsAreCanonical(t *testing.T) {
	upper := "00000000-0000-4000-8000-00000000ABCD"
	lower := "00000000-0000-4000-8000-00000000abcd"
	root := filepath.Join("agent-transcripts", upper, lower+".jsonl")
	child := filepath.Join("agent-transcripts", upper, "subagents", upper+".jsonl")

	if !IsTranscript(root) {
		t.Fatalf("mixed-case root path %q was rejected", root)
	}
	if got := IDFromPath(root); got != lower {
		t.Fatalf("IDFromPath() = %q, want %q", got, lower)
	}
	if got := ParentIDFromPath(child); got != lower {
		t.Fatalf("ParentIDFromPath() = %q, want %q", got, lower)
	}
}

type statReadSource struct{ info fs.FileInfo }

func (s statReadSource) Open(string) (io.ReadCloser, error) {
	return nil, errors.New("unexpected Open")
}

func (s statReadSource) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, errors.New("unexpected ReadDir")
}

func (s statReadSource) Stat(string) (fs.FileInfo, error) { return s.info, nil }

type cancelingStatSource struct {
	statReadSource
	cancel   context.CancelFunc
	cancelAt int
	calls    int
}

func (source *cancelingStatSource) Stat(string) (fs.FileInfo, error) {
	source.calls++
	if source.calls == source.cancelAt {
		source.cancel()
	}
	return source.info, nil
}

func TestSelectCursorFilesStopsDuringCandidateLimiting(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(tempFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, vendors.MaxCandidateFilesPerAgent+1)
	for i := range files {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", i)
		files[i] = filepath.Join("agent-transcripts", id, id+".jsonl")
	}
	ctx, cancel := context.WithCancel(context.Background())
	source := &cancelingStatSource{statReadSource: statReadSource{info: info}, cancel: cancel, cancelAt: len(files) + 1}

	_, err = selectCursorFilesSourceWithMetadataContext(ctx, source, files, 1, vendors.EmptySessionMetadata())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if source.calls != len(files)+1 {
		t.Fatalf("Stat calls = %d, want %d", source.calls, len(files)+1)
	}
}
