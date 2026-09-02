package remote

import (
	"fmt"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const testModel = "claude-sonnet-4-20250514"

func writeClaudeFixture(fs *fakeFS, projectDir, id string, inTokens, outTokens int, modTime time.Time) string {
	filePath := path.Join(fakeHome, ".claude/projects", projectDir, id+".jsonl")
	content := fmt.Sprintf(
		`{"type":"user","uuid":"row:prompt","sessionId":%q,"timestamp":"2026-08-18T10:00:00.000Z","cwd":"/test/project","message":{"content":"do work"}}
{"type":"assistant","uuid":"row:msg_1","sessionId":%q,"timestamp":"2026-08-18T10:00:01.000Z","durationMs":500,"message":{"id":"msg_1","model":%q,"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`, id, id, testModel, inTokens, outTokens)
	fs.writeFile(filePath, content, modTime)
	return filePath
}

func writeCodexFixture(fs *fakeFS, id, parentID string, modTime time.Time) string {
	filePath := path.Join(fakeHome, ".codex/sessions/2026/08/18", "rollout-2026-08-18T10-00-00-"+id+".jsonl")
	parent := ""
	if parentID != "" {
		parent = fmt.Sprintf(`,"parent_thread_id":%q`, parentID)
	}
	content := fmt.Sprintf(
		`{"timestamp":"2026-08-18T10:00:00.000Z","type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":"/test/project"%s}}
{"timestamp":"2026-08-18T10:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"do work"}}
{"timestamp":"2026-08-18T10:00:02.000Z","type":"event_msg","payload":{"type":"task_started"}}
`, id, id, parent)
	fs.writeFile(filePath, content, modTime)
	return filePath
}

func TestCollectIncrementalHandlesMissingVendorRoots(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(*fakeFS)
		wantFamilies int
	}{
		{name: "neither vendor installed", wantFamilies: 0},
		{
			name: "Claude only",
			setup: func(fs *fakeFS) {
				writeClaudeFixture(fs, "project", "aaaaaaaa-0000-0000-0000-000000000001", 1, 1, time.Unix(1000, 0))
			},
			wantFamilies: 1,
		},
		{
			name: "Codex only",
			setup: func(fs *fakeFS) {
				writeCodexFixture(fs, "11111111-2222-3333-4444-555555555555", "", time.Unix(1000, 0))
			},
			wantFamilies: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fs := newFakeFS()
			if test.setup != nil {
				test.setup(fs)
			}
			snapshot, sessions, failures, err := collectIncremental(
				newFakeSource(fs, Limits{}), 0, time.Unix(2000, 0), CachedSnapshotV2{},
			)
			if err != nil {
				t.Fatalf("collectIncremental: %v", err)
			}
			if len(failures) != 0 {
				t.Fatalf("missing optional vendor root produced failures: %v", failures)
			}
			if len(snapshot.Families) != test.wantFamilies || len(sessions) != test.wantFamilies {
				t.Fatalf("families=%d sessions=%d, want %d", len(snapshot.Families), len(sessions), test.wantFamilies)
			}
		})
	}
}

func TestCollectIncrementalSkipsUnchangedFamiliesAndHeaders(t *testing.T) {
	fs := newFakeFS()
	claudeID := "aaaaaaaa-0000-0000-0000-000000000001"
	codexID := "11111111-2222-3333-4444-555555555555"
	claudePath := writeClaudeFixture(fs, "proj1", claudeID, 10, 5, time.Unix(1000, 0))
	codexPath := writeCodexFixture(fs, codexID, "", time.Unix(1000, 0))
	source := newFakeSource(fs, Limits{})

	snapshot, sessions, failures, err := collectIncremental(source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
	if err != nil {
		t.Fatalf("cold collectIncremental: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (one claude root, one codex root)", len(sessions))
	}
	if len(snapshot.Families) != 2 {
		t.Fatalf("cached families = %d, want 2", len(snapshot.Families))
	}

	// Warm refresh: nothing on disk changed, so neither transcript body nor
	// the Codex header should be reopened.
	before := fs.openCounts()
	snapshot2, sessions2, failures2, err := collectIncremental(source, 0, time.Unix(3000, 0), snapshot)
	if err != nil {
		t.Fatalf("warm collectIncremental: %v", err)
	}
	if len(failures2) != 0 {
		t.Fatalf("unexpected failures on warm refresh: %v", failures2)
	}
	if len(sessions2) != 2 {
		t.Fatalf("warm sessions = %d, want 2", len(sessions2))
	}
	after := fs.openCounts()
	if after[claudePath] != before[claudePath] {
		t.Fatalf("unchanged Claude family body was reopened: before=%d after=%d", before[claudePath], after[claudePath])
	}
	if after[codexPath] != before[codexPath] {
		t.Fatalf("unchanged Codex header was reopened: before=%d after=%d", before[codexPath], after[codexPath])
	}
	if snapshot2.CoverageSinceMs != 0 {
		t.Fatalf("coverage since = %d, want 0 (request completed fully)", snapshot2.CoverageSinceMs)
	}
	if len(snapshot2.Families) != 2 {
		t.Fatalf("warm cached families = %d, want 2", len(snapshot2.Families))
	}
}

func TestCollectIncrementalIsolatesCorruptFamily(t *testing.T) {
	fs := newFakeFS()
	goodID := "aaaaaaaa-0000-0000-0000-000000000002"
	badID := "bbbbbbbb-0000-0000-0000-000000000003"
	writeClaudeFixture(fs, "proj1", goodID, 1, 1, time.Unix(1000, 0))
	fs.writeFile(path.Join(fakeHome, ".claude/projects/proj1", badID+".jsonl"), "{not valid jsonl", time.Unix(1000, 0))
	source := newFakeSource(fs, Limits{})

	snapshot, sessions, failures, err := collectIncremental(source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
	if err != nil {
		t.Fatalf("collectIncremental: %v", err)
	}
	if len(failures) == 0 {
		t.Fatal("a corrupt family must make refresh health partial")
	}
	foundGood := false
	for _, s := range sessions {
		if s.ID == goodID {
			foundGood = true
		}
		if s.ID == badID {
			t.Fatalf("corrupt family %s should not appear as a session", badID)
		}
	}
	if !foundGood {
		t.Fatalf("valid family %s was hidden by the unrelated corrupt family", goodID)
	}
	for _, family := range snapshot.Families {
		if family.FamilyID == badID {
			t.Fatalf("corrupt family must not be committed to the cache")
		}
	}
}

func TestCollectIncrementalRejectsFamilyChangedDuringParse(t *testing.T) {
	fs := newFakeFS()
	id := "aaaaaaaa-0000-0000-0000-000000000009"
	file := writeClaudeFixture(fs, "proj1", id, 1, 1, time.Unix(1000, 0))
	var once sync.Once
	fs.onOpen = func(opened string) {
		if opened == file {
			once.Do(func() {
				writeClaudeFixture(fs, "proj1", id, 2, 2, time.Unix(1001, 0))
			})
		}
	}
	source := newFakeSource(fs, Limits{})

	snapshot, _, failures, err := collectIncremental(source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
	if err != nil {
		t.Fatalf("collectIncremental: %v", err)
	}
	if len(failures) == 0 {
		t.Fatal("unstable family was not reported as partial")
	}
	if len(snapshot.Families) != 0 {
		t.Fatal("facts read while the file changed must not be committed")
	}
}

func TestCollectIncrementalReparsesOldParserVersion(t *testing.T) {
	fs := newFakeFS()
	id := "aaaaaaaa-0000-0000-0000-000000000010"
	file := writeClaudeFixture(fs, "proj1", id, 1, 1, time.Unix(1000, 0))
	source := newFakeSource(fs, Limits{})
	baseline, _, _, err := collectIncremental(source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
	if err != nil {
		t.Fatal(err)
	}
	for index := range baseline.Families {
		if baseline.Families[index].Vendor == vendors.AgentClaude {
			baseline.Families[index].Facts.ParserVersion = "claude-sftp/old"
		}
	}
	before := fs.openCounts()[file]
	_, _, _, err = collectIncremental(source, 0, time.Unix(3000, 0), baseline)
	if err != nil {
		t.Fatal(err)
	}
	if after := fs.openCounts()[file]; after <= before {
		t.Fatal("unchanged fingerprint incorrectly reused facts from an old parser version")
	}
}

func TestCollectIncrementalFallsBackToFullCollectionWhenKnownSetIsTooLarge(t *testing.T) {
	fs := newFakeFS()
	fs.mu.Lock()
	fs.mkdirAll(path.Join(fakeHome, ".claude/projects"))
	fs.mkdirAll(path.Join(fakeHome, ".codex/sessions"))
	fs.mu.Unlock()
	baseline := CachedSnapshotV2{Version: cacheV2Version, BaselineID: "large-baseline"}
	for index := 0; index <= remoteprotocol.MaxKnownFamilies; index++ {
		id := fmt.Sprintf("family-%04d", index)
		baseline.Families = append(baseline.Families, CachedFamilyV2{
			Vendor: vendors.AgentClaude, FamilyID: id, Fingerprint: fmt.Sprintf("fp-%04d", index),
		})
	}

	snapshot, _, _, err := collectIncremental(newFakeSource(fs, Limits{}), 0, time.Unix(2000, 0), baseline)
	if err != nil {
		t.Fatalf("baseline-free fallback failed: %v", err)
	}
	if len(snapshot.Families) != 0 {
		t.Fatalf("baseline-free full collection retained %d stale cached families", len(snapshot.Families))
	}
}

func TestCollectIncrementalNeverTombstonesOnHardVendorFailure(t *testing.T) {
	fs := newFakeFS()
	claudeID := "aaaaaaaa-0000-0000-0000-000000000004"
	writeClaudeFixture(fs, "proj1", claudeID, 1, 1, time.Unix(1000, 0))
	source := newFakeSource(fs, Limits{})

	firstSnapshot, _, _, err := collectIncremental(source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
	if err != nil || len(firstSnapshot.Families) != 1 {
		t.Fatalf("seed refresh: snapshot=%+v err=%v", firstSnapshot, err)
	}

	// Simulate a codex root directory that has become unreadable: any file
	// under it now looks like a directory to lstat, so FilesSource fails.
	fs.mu.Lock()
	fs.files[path.Join(fakeHome, ".codex/sessions")] = &fakeEntry{isDir: true, symlink: true}
	fs.mu.Unlock()

	secondSnapshot, _, failures, err := collectIncremental(source, 0, time.Unix(3000, 0), firstSnapshot)
	if err != nil {
		t.Fatalf("a single hard vendor failure should not fail the whole refresh: %v", err)
	}
	if len(failures) == 0 {
		t.Fatal("expected a reported Codex failure")
	}
	foundClaude := false
	for _, family := range secondSnapshot.Families {
		if family.FamilyID == claudeID {
			foundClaude = true
		}
	}
	if !foundClaude {
		t.Fatal("an incomplete Codex scan must not remove the unrelated, already-cached Claude family")
	}
	if secondSnapshot.CoverageSinceMs != 0 {
		t.Fatalf("coverage should not advance past the prior baseline on a partial refresh: got %d", secondSnapshot.CoverageSinceMs)
	}
}

func TestCollectIncrementalTombstonesGenuinelyDeletedFamily(t *testing.T) {
	fs := newFakeFS()
	goneID := "aaaaaaaa-0000-0000-0000-000000000007"
	stayID := "bbbbbbbb-0000-0000-0000-000000000008"
	writeClaudeFixture(fs, "proj1", goneID, 1, 1, time.Unix(1000, 0))
	writeClaudeFixture(fs, "proj1", stayID, 1, 1, time.Unix(1000, 0))
	source := newFakeSource(fs, Limits{})

	baseline, _, _, err := collectIncremental(source, 0, time.Unix(1500, 0), CachedSnapshotV2{})
	if err != nil || len(baseline.Families) != 2 {
		t.Fatalf("seed refresh: snapshot=%+v err=%v", baseline, err)
	}

	fs.mu.Lock()
	delete(fs.files, path.Join(fakeHome, ".claude/projects/proj1", goneID+".jsonl"))
	fs.mu.Unlock()

	after, _, _, err := collectIncremental(source, 0, time.Unix(2500, 0), baseline)
	if err != nil {
		t.Fatalf("collectIncremental: %v", err)
	}
	for _, family := range after.Families {
		if family.FamilyID == goneID {
			t.Fatal("a family whose only file was removed from a completely enumerated tree must be tombstoned")
		}
	}
	foundStay := false
	for _, family := range after.Families {
		if family.FamilyID == stayID {
			foundStay = true
		}
	}
	if !foundStay {
		t.Fatal("deleting one family must not remove an unrelated family")
	}
}

func TestCollectIncrementalPreservesFamilyOutsideNarrowerWindow(t *testing.T) {
	fs := newFakeFS()
	oldID := "aaaaaaaa-0000-0000-0000-000000000005"
	newID := "bbbbbbbb-0000-0000-0000-000000000006"
	writeClaudeFixture(fs, "proj1", oldID, 1, 1, time.Unix(1000, 0))
	source := newFakeSource(fs, Limits{})

	baseline, _, _, err := collectIncremental(source, 0, time.Unix(1500, 0), CachedSnapshotV2{})
	if err != nil || len(baseline.Families) != 1 {
		t.Fatalf("seed refresh: snapshot=%+v err=%v", baseline, err)
	}

	// A later refresh with a narrower window (since far in the future) must
	// not tombstone the old family: it still exists on disk, just outside
	// the requested display window.
	writeClaudeFixture(fs, "proj1", newID, 2, 2, time.Unix(9_000_000, 0))
	narrow, sessions, _, err := collectIncremental(source, 8_000_000_000, time.Unix(9_000_001, 0), baseline)
	if err != nil {
		t.Fatalf("collectIncremental: %v", err)
	}
	foundOld := false
	for _, family := range narrow.Families {
		if family.FamilyID == oldID {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatal("a family outside the requested window must not be tombstoned")
	}
	_ = sessions
	_ = vendors.AgentClaude
}
