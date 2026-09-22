package remote

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionrecord"
	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remotehelper"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

const testModel = "claude-sonnet-4-20250514"

func TestBoundChangedRecordConvertsOversizedFamilyToStructuredSkip(t *testing.T) {
	record := remoteprotocol.Record{
		Type: remoteprotocol.RecordChanged, Vendor: vendors.AgentCodex, FamilyID: "root", Fingerprint: "new",
		Family: &remotefacts.Family{Sessions: []remotefacts.Session{{ID: "root", Display: session.Session{Summary: pointerTo(strings.Repeat("x", 4<<10))}}}},
	}
	bounded, limited, _, err := boundChangedRecord(record, "request-1", 2, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !limited || bounded.Type != remoteprotocol.RecordSkipped || bounded.FamilyID != "root" || bounded.Reason != remotefacts.StaleReasonVendorBudgetExceeded {
		t.Fatalf("bounded record = %#v limited=%v", bounded, limited)
	}
}

func TestSortVendorRecordsWrapsAfterCursor(t *testing.T) {
	records := []remoteprotocol.Record{{FamilyID: "b"}, {FamilyID: "d"}, {FamilyID: "a"}, {FamilyID: "c"}}
	sortVendorRecords(records, "b")
	for index, want := range []string{"c", "d", "a", "b"} {
		if records[index].FamilyID != want {
			t.Fatalf("paged record %d = %q, want %q", index, records[index].FamilyID, want)
		}
	}
}

func pointerTo(value string) *string { return &value }

func TestSFTPFamilyFingerprintIncludesEverySessionMetadata(t *testing.T) {
	metadata := vendors.EmptySessionMetadata()
	metadata.Session("root").Name = "before"
	metadata.Session("child").Name = "before"
	in := vendorFamilyInput{
		Metadata: metadata, SessionIDs: map[string][]string{"root": {"root", "child"}},
	}
	fingerprints := []vendors.FileFingerprint{{Key: "opaque", Size: 1, ModifiedAtMs: 2}}
	before := familyFingerprint(in, "root", fingerprints)
	metadata.Session("child").Name = "after"
	if after := familyFingerprint(in, "root", fingerprints); after == before {
		t.Fatal("metadata-only change did not change SFTP family fingerprint")
	}
}

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

// A fork keeps the root identity in session_meta; only the filename names the
// thread it holds.
func writeForkedCodexFixture(fs *fakeFS, rootID, threadID string, modTime time.Time) string {
	filePath := path.Join(fakeHome, ".codex/sessions/2026/08/18", "rollout-2026-08-18T10-00-00-"+rootID+"_"+threadID+".jsonl")
	content := fmt.Sprintf(
		`{"timestamp":"2026-08-18T10:00:00.000Z","type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":"/test/project"}}
{"timestamp":"2026-08-18T10:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"do forked work"}}
{"timestamp":"2026-08-18T10:00:02.000Z","type":"event_msg","payload":{"type":"task_started"}}
`, rootID, rootID)
	fs.writeFile(filePath, content, modTime)
	return filePath
}

func completeCodexFixture(id string) string {
	return fmt.Sprintf(
		`{"timestamp":"2026-08-18T10:00:00.000Z","type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":"/test/project","git":{"branch":"main"}}}
{"timestamp":"2026-08-18T10:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"do complete work"}}
{"timestamp":"2026-08-18T10:00:02.000Z","type":"turn_context","payload":{"model":"gpt-5"}}
{"timestamp":"2026-08-18T10:00:03.000Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-08-18T10:00:04.000Z","type":"event_msg","payload":{"type":"patch_apply_end","changes":{"main.go":{"type":"update","unified_diff":"@@\n-old\n+new\n"},"new.go":{"type":"add","content":"package newfile\n"}}}}
{"timestamp":"2026-08-18T10:00:05.000Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"done"}}
{"timestamp":"2026-08-18T10:00:06.000Z","type":"event_msg","payload":{"type":"task_complete"}}
`, id, id)
}

func writeCompleteCodexFixture(fs *fakeFS, id string, modTime time.Time) string {
	filePath := path.Join(fakeHome, ".codex/sessions/2026/08/18", "rollout-2026-08-18T10-00-00-"+id+".jsonl")
	fs.writeFile(filePath, completeCodexFixture(id), modTime)
	return filePath
}

func TestCodexSessionMetaOnlyRootIsAbsentFromCompleteInventory(t *testing.T) {
	id := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	content := fmt.Sprintf(
		`{"timestamp":"2026-08-18T10:00:00.000Z","type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":"/test/project"}}
`, id, id)
	modTime := time.Unix(2_000, 0)
	fake := newFakeFS()
	file := path.Join(fakeHome, ".codex/sessions/2026/08/18", "rollout-2026-08-18T10-00-00-"+id+".jsonl")
	fake.writeFile(file, content, modTime)

	snapshot, sessions, failures, err := collectIncremental(
		context.Background(), newFakeSource(fake, Limits{}), 0, time.Unix(3_000, 0), CachedSnapshotV2{SourceID: "r_0123456789abcdef"},
	)
	if err != nil || len(failures) != 0 || !snapshot.RequestComplete || len(snapshot.Families) != 0 || len(sessions) != 0 {
		t.Fatalf("SFTP meta-only collection: snapshot=%#v sessions=%d failures=%v err=%v", snapshot, len(sessions), failures, err)
	}

	home := t.TempDir()
	realFile := filepath.Join(home, ".codex", "sessions", "2026", "08", "18", filepath.Base(file))
	if err := os.MkdirAll(filepath.Dir(realFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	request := remoteprotocol.Request{
		RequestID: "helper-meta-only", Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1},
		Schema: remoteprotocol.VersionRange{Min: 2, Max: 2}, ParserVersion: vendors.ParserVersion,
		SourceID: "r_0123456789abcdef", BaselineMode: remoteprotocol.BaselineNone,
		CollectedAtMs: time.Unix(3_000, 0).UnixMilli(), Vendors: []string{vendors.AgentCodex},
		Limits: remoteprotocol.Limits{MaxRecordBytes: remoteprotocol.MaxRecordBytes, MaxResponseBytes: remoteprotocol.MaxResponseBytes, MaxRecords: remoteprotocol.MaxRecords, MaxInventoryFamilies: remoteprotocol.MaxInventoryFamilies},
	}
	var output bytes.Buffer
	outcome, err := remotehelper.Collect(t.Context(), request, remotehelper.Options{Home: home}, &output)
	if err != nil || !outcome.RequestComplete {
		t.Fatalf("helper meta-only collection: outcome=%#v err=%v", outcome, err)
	}
	records, err := remoteprotocol.Decode(&output, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Type == remoteprotocol.RecordChanged || record.Type == remoteprotocol.RecordSkipped || len(record.Inventory) != 0 {
			t.Fatalf("helper published meta-only root: %#v", record)
		}
	}
}

func TestCodexForkedRolloutCollectsAsDistinctFamily(t *testing.T) {
	rootID := "11111111-2222-3333-4444-555555555555"
	threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	fake := newFakeFS()
	writeCodexFixture(fake, rootID, "", time.Unix(1_000, 0))
	writeForkedCodexFixture(fake, rootID, threadID, time.Unix(2_000, 0))

	snapshot, sessions, failures, err := collectIncremental(
		context.Background(), newFakeSource(fake, Limits{}), 0, time.Unix(3_000, 0), CachedSnapshotV2{SourceID: "r_0123456789abcdef"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 || !snapshot.RequestComplete {
		t.Fatalf("forked collection incomplete: failures=%v snapshot=%#v", failures, snapshot)
	}
	if len(snapshot.Families) != 2 {
		t.Fatalf("forked families = %#v, want distinct root and fork families", snapshot.Families)
	}
	if len(sessions) != 2 {
		t.Fatalf("top-level sessions = %#v, want root and fork", sessions)
	}
	parents := map[string]string{}
	familyIDs := map[string]bool{}
	for _, family := range snapshot.Families {
		familyIDs[family.FamilyID] = true
		for _, item := range family.Facts.Sessions {
			parents[item.ID] = item.ParentID
		}
	}
	if !familyIDs[rootID] || !familyIDs[threadID] || len(parents) != 2 || parents[rootID] != "" || parents[threadID] != "" {
		t.Fatalf("collected thread parentage = %#v", parents)
	}
}

func TestCodexFullRecordMatchesLocalHelperAndSFTPAndSurvivesWarmRefresh(t *testing.T) {
	id := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	modTime := time.Unix(2_000, 0)
	fake := newFakeFS()
	file := writeCompleteCodexFixture(fake, id, modTime)
	index := fmt.Sprintf("{\"id\":%q,\"thread_name\":\"Complete fixture\"}\n", id)
	fake.writeFile(path.Join(fakeHome, ".codex", "session_index.jsonl"), index, modTime)
	baseline := CachedSnapshotV2{SourceID: "r_0123456789abcdef"}
	sftp, _, failures, err := collectIncremental(context.Background(), newFakeSource(fake, Limits{}), 0, time.Unix(3_000, 0), baseline)
	if err != nil || len(failures) != 0 || len(sftp.FullRecords) != 1 {
		t.Fatalf("SFTP collect: records=%d failures=%v err=%v", len(sftp.FullRecords), failures, err)
	}
	changes := sftp.FullRecords[0].Record.Session.FileEdits
	if len(changes) != 2 || len(changes[0].Changes) != 1 || len(changes[1].Changes) != 1 ||
		changes[0].Changes[0].Text != "@@\n-old\n+new\n" || changes[1].Changes[0].Text != "@@\n+package newfile\n" {
		t.Fatalf("ordered SFTP changes = %#v", changes)
	}

	before := fake.openCounts()[file]
	warm, _, failures, err := collectIncremental(context.Background(), newFakeSource(fake, Limits{}), 0, time.Unix(4_000, 0), sftp)
	if err != nil || len(failures) != 0 || len(warm.FullRecords) != 1 ||
		warm.FullRecords[0].Record.RevisionID != sftp.FullRecords[0].Record.RevisionID {
		t.Fatalf("warm collect = %#v failures=%v err=%v", warm.FullRecords, failures, err)
	}
	if after := fake.openCounts()[file]; after != before {
		t.Fatalf("warm refresh reopened transcript: before=%d after=%d", before, after)
	}
	legacy := sftp
	legacy.Version = legacyCacheV2Version
	legacy.SourceID = ""
	legacy.FullRecords = nil
	beforeMigration := fake.openCounts()[file]
	migrated, _, failures, err := collectIncremental(context.Background(), newFakeSource(fake, Limits{}), 0, time.Unix(4_500, 0), legacy)
	if err != nil || len(failures) != 0 || len(migrated.FullRecords) != 1 {
		t.Fatalf("legacy migration: records=%d failures=%v err=%v", len(migrated.FullRecords), failures, err)
	}
	if after := fake.openCounts()[file]; after <= beforeMigration {
		t.Fatal("legacy unchanged family was not recollected for its complete record")
	}

	home := t.TempDir()
	realFile := filepath.Join(home, ".codex", "sessions", "2026", "08", "18", filepath.Base(file))
	if err := os.MkdirAll(filepath.Dir(realFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realFile, []byte(completeCodexFixture(id)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(realFile, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	indexFile := filepath.Join(home, ".codex", "session_index.jsonl")
	if err := os.WriteFile(indexFile, []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	localParsed, localMetadata, err := codex.CollectContext(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	localRecords, err := fullsessionrecord.FromParsedFamily(
		baseline.SourceID, vendors.AgentCodex, vendors.LocalReadSource, localParsed, localMetadata,
	)
	if err != nil || len(localRecords) != 1 {
		t.Fatalf("local records=%d err=%v", len(localRecords), err)
	}
	request := remoteprotocol.Request{
		RequestID: "helper-full-1", Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1},
		Schema: remoteprotocol.VersionRange{Min: 2, Max: 2}, ParserVersion: vendors.ParserVersion,
		SourceID: baseline.SourceID, BaselineMode: remoteprotocol.BaselineNone,
		CollectedAtMs: time.Unix(3_000, 0).UnixMilli(), Vendors: []string{vendors.AgentCodex},
		Limits: remoteprotocol.Limits{MaxRecordBytes: remoteprotocol.MaxRecordBytes, MaxResponseBytes: remoteprotocol.MaxResponseBytes, MaxRecords: remoteprotocol.MaxRecords, MaxInventoryFamilies: remoteprotocol.MaxInventoryFamilies},
	}
	var output bytes.Buffer
	if _, err := remotehelper.Collect(t.Context(), request, remotehelper.Options{Home: home}, &output); err != nil {
		t.Fatal(err)
	}
	records, err := remoteprotocol.Decode(&output, request)
	if err != nil {
		t.Fatal(err)
	}
	var helperRecord *fullsessionv1.Record
	for _, record := range records {
		if len(record.FullRecords) == 1 {
			value := record.FullRecords[0].Record
			helperRecord = &value
		}
	}
	if helperRecord == nil || !reflect.DeepEqual(*helperRecord, sftp.FullRecords[0].Record) ||
		!reflect.DeepEqual(localRecords[0], sftp.FullRecords[0].Record) {
		t.Fatalf("local/helper/SFTP full records differ\nlocal=%#v\nhelper=%#v\nsftp=%#v", localRecords[0], helperRecord, sftp.FullRecords[0].Record)
	}
	localBytes, _ := fullsessionv1.Marshal(localRecords[0])
	helperBytes, _ := fullsessionv1.Marshal(*helperRecord)
	sftpBytes, _ := fullsessionv1.Marshal(sftp.FullRecords[0].Record)
	if !bytes.Equal(localBytes, helperBytes) || !bytes.Equal(localBytes, sftpBytes) {
		t.Fatal("canonical local/helper/SFTP record bytes differ")
	}
}

func TestCodexRemoteFamiliesSelectRecentChildOfOldRoot(t *testing.T) {
	fs := newFakeFS()
	rootID := "11111111-2222-3333-4444-555555555555"
	childID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	root := writeCodexFixture(fs, rootID, "", time.Unix(1000, 0))
	child := writeCodexFixture(fs, childID, rootID, time.Unix(2000, 0))

	families, _, _, _, _, _, _, _, err := codex.BuildRemoteFamilies(
		newFakeSource(fs, Limits{}), fakeHome, time.Unix(1500, 0).UnixMilli(), nil, nil,
	)
	if err != nil {
		t.Fatalf("BuildRemoteFamilies: %v", err)
	}
	family, ok := families[rootID]
	if !ok {
		t.Fatalf("recent child did not select its old root family: %#v", families)
	}
	if len(family.Files) != 2 || family.Files[0] != root || family.Files[1] != child {
		t.Fatalf("family files = %#v, want root and child", family.Files)
	}
}

func TestCodexRemoteFamiliesSelectLiveChildOfOldRoot(t *testing.T) {
	fs := newFakeFS()
	rootID := "22222222-3333-4444-5555-666666666666"
	childID := "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	writeCodexFixture(fs, rootID, "", time.Unix(1000, 0))
	writeCodexFixture(fs, childID, rootID, time.Unix(1000, 0))

	families, _, _, _, _, _, _, _, err := codex.BuildRemoteFamilies(
		newFakeSource(fs, Limits{}), fakeHome, time.Unix(1500, 0).UnixMilli(),
		map[string]string{childID: "interactive"}, nil,
	)
	if err != nil {
		t.Fatalf("BuildRemoteFamilies: %v", err)
	}
	if family, ok := families[rootID]; !ok || len(family.Files) != 2 {
		t.Fatalf("live child did not select its old root family: %#v", families)
	}
}

func TestCodexRemoteFamiliesExcludeOldUnreadableFileOutsideWindow(t *testing.T) {
	fs := newFakeFS()
	id := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	file := path.Join(fakeHome, ".codex/sessions/2026/08/18", "rollout-2026-08-18T10-00-00-"+id+".jsonl")
	fs.writeFile(file, "{not valid json", time.Unix(1000, 0))

	families, _, _, _, failed, _, _, _, err := codex.BuildRemoteFamilies(
		newFakeSource(fs, Limits{}), fakeHome, time.Unix(1500, 0).UnixMilli(), nil, nil,
	)
	if err != nil {
		t.Fatalf("BuildRemoteFamilies: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("header failures = %#v, want one unreadable file", failed)
	}
	if len(families) != 0 {
		t.Fatalf("old unreadable file bypassed the requested window: %#v", families)
	}
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
				context.Background(), newFakeSource(fs, Limits{}), 0, time.Unix(2000, 0), CachedSnapshotV2{},
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

	snapshot, sessions, failures, err := collectIncremental(context.Background(), source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
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
	snapshot2, sessions2, failures2, err := collectIncremental(context.Background(), source, 0, time.Unix(3000, 0), snapshot)
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

	snapshot, sessions, failures, err := collectIncremental(context.Background(), source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
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

	snapshot, _, failures, err := collectIncremental(context.Background(), source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
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
	baseline, _, _, err := collectIncremental(context.Background(), source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
	if err != nil {
		t.Fatal(err)
	}
	for index := range baseline.Families {
		if baseline.Families[index].Vendor == vendors.AgentClaude {
			baseline.Families[index].Facts.ParserVersion = "claude-sftp/old"
		}
	}
	before := fs.openCounts()[file]
	_, _, _, err = collectIncremental(context.Background(), source, 0, time.Unix(3000, 0), baseline)
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

	snapshot, _, _, err := collectIncremental(context.Background(), newFakeSource(fs, Limits{}), 0, time.Unix(2000, 0), baseline)
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

	firstSnapshot, _, _, err := collectIncremental(context.Background(), source, 0, time.Unix(2000, 0), CachedSnapshotV2{})
	if err != nil || len(firstSnapshot.Families) != 1 {
		t.Fatalf("seed refresh: snapshot=%+v err=%v", firstSnapshot, err)
	}

	// Simulate a codex root directory that has become unreadable: any file
	// under it now looks like a directory to lstat, so FilesSource fails.
	fs.mu.Lock()
	fs.files[path.Join(fakeHome, ".codex/sessions")] = &fakeEntry{isDir: true, symlink: true}
	fs.mu.Unlock()

	secondSnapshot, _, failures, err := collectIncremental(context.Background(), source, 0, time.Unix(3000, 0), firstSnapshot)
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

func TestCollectIncrementalNeverTombstonesAfterSkippedDirectory(t *testing.T) {
	fake := newFakeFS()
	retainedID := "aaaaaaaa-0000-0000-0000-000000000011"
	writeClaudeFixture(fake, "unreadable", retainedID, 1, 1, time.Unix(1000, 0))
	baseline, _, _, err := collectIncremental(context.Background(), newFakeSource(fake, Limits{}), 0, time.Unix(2000, 0), CachedSnapshotV2{})
	if err != nil || len(baseline.Families) != 1 {
		t.Fatalf("seed refresh: snapshot=%+v err=%v", baseline, err)
	}

	unreadable := path.Join(fakeHome, ".claude/projects/unreadable")
	ops := fake.ops()
	readDir := ops.readDir
	ops.readDir = func(name string) ([]os.FileInfo, error) {
		if name == unreadable {
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
		}
		return readDir(name)
	}
	source, err := newSource(ops, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	after, _, failures, err := collectIncremental(context.Background(), source, time.Unix(2500, 0).UnixMilli(), time.Unix(3000, 0), baseline)
	if err != nil {
		t.Fatalf("partial refresh: %v", err)
	}
	if len(after.Families) != 1 || after.Families[0].FamilyID != retainedID {
		t.Fatalf("skipped directory tombstoned cached family: %+v", after.Families)
	}
	var claudeCoverage *AgentCoverage
	for index := range after.Coverage {
		if after.Coverage[index].Agent == vendors.AgentClaude {
			claudeCoverage = &after.Coverage[index]
		}
	}
	if claudeCoverage == nil || claudeCoverage.SkippedEntries != 1 {
		t.Fatalf("skipped directory coverage = %+v", claudeCoverage)
	}
	if len(failures) == 0 {
		t.Fatal("skipped directory was not reported as partial collection")
	}
	if after.CoverageSinceMs != baseline.CoverageSinceMs {
		t.Fatalf("incomplete enumeration advanced coverage from %d to %d", baseline.CoverageSinceMs, after.CoverageSinceMs)
	}
}

func TestCollectIncrementalTombstonesGenuinelyDeletedFamily(t *testing.T) {
	fs := newFakeFS()
	goneID := "aaaaaaaa-0000-0000-0000-000000000007"
	stayID := "bbbbbbbb-0000-0000-0000-000000000008"
	writeClaudeFixture(fs, "proj1", goneID, 1, 1, time.Unix(1000, 0))
	writeClaudeFixture(fs, "proj1", stayID, 1, 1, time.Unix(1000, 0))
	source := newFakeSource(fs, Limits{})

	baseline, _, _, err := collectIncremental(context.Background(), source, 0, time.Unix(1500, 0), CachedSnapshotV2{})
	if err != nil || len(baseline.Families) != 2 {
		t.Fatalf("seed refresh: snapshot=%+v err=%v", baseline, err)
	}

	fs.mu.Lock()
	delete(fs.files, path.Join(fakeHome, ".claude/projects/proj1", goneID+".jsonl"))
	fs.mu.Unlock()

	after, _, _, err := collectIncremental(context.Background(), source, 0, time.Unix(2500, 0), baseline)
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

	baseline, _, _, err := collectIncremental(context.Background(), source, 0, time.Unix(1500, 0), CachedSnapshotV2{})
	if err != nil || len(baseline.Families) != 1 {
		t.Fatalf("seed refresh: snapshot=%+v err=%v", baseline, err)
	}

	// A later refresh with a narrower window (since far in the future) must
	// not tombstone the old family: it still exists on disk, just outside
	// the requested display window.
	writeClaudeFixture(fs, "proj1", newID, 2, 2, time.Unix(9_000_000, 0))
	narrow, sessions, _, err := collectIncremental(context.Background(), source, 8_000_000_000, time.Unix(9_000_001, 0), baseline)
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
