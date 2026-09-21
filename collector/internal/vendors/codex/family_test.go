package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestParseFamilyFilesSourceDoesNotExposePromptAsName(t *testing.T) {
	home := t.TempDir()
	id := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	file := filepath.Join(home, ".codex", "sessions", "rollout-2026-07-10T14-11-18-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + id + `","session_id":"` + id + `"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:11:19Z","type":"event_msg","payload":{"type":"user_message","message":"private first prompt"}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseFamilyFilesSource(vendors.LocalReadSource, home, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 || parsed[0].Name != "" {
		t.Fatalf("remote parsed name exposed prompt: %#v", parsed)
	}
}

func TestParseTranscriptCountsTopLevelCompactions(t *testing.T) {
	id := "019f4dde-db5b-7100-bdc0-09b5aaaac56f"
	file := filepath.Join(t.TempDir(), "rollout-2026-07-10T14-11-18-"+id+".jsonl")
	content := `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + id + `","session_id":"` + id + `"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:11:19Z","type":"compacted","payload":{"window_number":1}}` + "\n" +
		`{"timestamp":"2026-07-10T14:11:20Z","type":"compacted","payload":{"window_number":2}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseTranscript(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Session.Compactions; got != 2 {
		t.Fatalf("compactions = %d, want 2", got)
	}
}

func TestForkedRolloutUsesThreadIDDespiteRootSessionID(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	firstForkID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	secondForkID := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	root := writeFamilyTestRollout(t, home, rootID, rootID)
	firstFork := writeFamilyTestRollout(t, home, rootID+"_"+firstForkID, rootID)
	secondFork := writeFamilyTestRollout(t, home, rootID+"_"+secondForkID, rootID)

	if got := SessionIDFromRollout(firstFork); got != firstForkID {
		t.Fatalf("forked rollout ID = %q, want thread ID %q", got, firstForkID)
	}
	files := []string{root, firstFork, secondFork}
	headers := HeadersSource(vendors.LocalReadSource, files)
	for file, want := range map[string]FileHeader{
		root:       {SessionID: rootID},
		firstFork:  {SessionID: firstForkID},
		secondFork: {SessionID: secondForkID},
	} {
		got := headers[file]
		if got.Err != nil || got.SessionID != want.SessionID || got.ParentID != want.ParentID {
			t.Fatalf("header for %q = %#v, want %#v", filepath.Base(file), got, want)
		}
	}
	// A fork is a sibling branch, not a child: remotefacts reparents any
	// non-root family member onto the family ID, so each fork stands alone.
	for file, want := range map[string]string{
		root: rootID, firstFork: firstForkID, secondFork: secondForkID,
	} {
		if familyID := FamilyRoots(headers)[file]; familyID != want {
			t.Fatalf("family root for %q = %q, want %q", filepath.Base(file), familyID, want)
		}
	}

	parsed, err := ParseFamilyFilesSource(vendors.LocalReadSource, home, files)
	if err != nil {
		t.Fatal(err)
	}
	parents := map[string]string{}
	for _, item := range parsed {
		parents[item.Session.ID] = item.ParentID
	}
	if len(parents) != 3 || parents[rootID] != "" || parents[firstForkID] != "" || parents[secondForkID] != "" {
		t.Fatalf("parsed thread parentage = %#v", parents)
	}
}

// Codex Desktop writes forks without history_base, and remote sessions carry
// no client metadata worth matching on, so the filename alone must identify
// them: a rejected header marks the whole family skipped and drops the fork.
func TestForkedRolloutWithoutClientMetadataUsesThreadID(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	file := writeFamilyTestRollout(t, home, rootID+"_"+threadID, rootID)

	id, parentID, err := readHeader(file)
	if err != nil || id != threadID || parentID != "" {
		t.Fatalf("readHeader = %q, %q, %v; want thread ID %q", id, parentID, err, threadID)
	}
}

func TestChainedForkedRolloutUsesFinalThreadID(t *testing.T) {
	rootID := "11111111-2222-3333-4444-555555555555"
	middleID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	threadID := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	// Whichever generation labelled the first row, the filename's last ID is
	// the thread the file holds.
	for _, headerID := range []string{rootID, middleID, threadID} {
		t.Run(headerID, func(t *testing.T) {
			home := t.TempDir()
			file := writeFamilyTestRollout(t, home, rootID+"_"+middleID+"_"+threadID, headerID)

			id, _, err := readHeader(file)
			if err != nil || id != threadID {
				t.Fatalf("readHeader = %q, %v; want thread ID %q", id, err, threadID)
			}
		})
	}
}

// A fork replays its ancestors' session_meta rows before its own, and Codex
// labels them all with the root ID. Reading lineage or context off an inlined
// ancestor hands the fork the wrong cwd, branch and parent.
func TestForkedRolloutPrefersItsOwnInlinedMeta(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	ancestorParentID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	dir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "rollout-2026-07-10T14-11-18-"+rootID+"_"+threadID+".jsonl")
	content := `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + rootID +
		`","session_id":"` + rootID + `","cwd":"/ancestor/dir","parent_thread_id":"` + ancestorParentID + `"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:12:18Z","type":"session_meta","payload":{"id":"` + rootID +
		`","session_id":"` + rootID + `","cwd":"/fork/dir","forked_from_id":"` + rootID + `"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:12:19Z","type":"event_msg","payload":{"type":"user_message","message":"fork work"}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	id, parentID, err := readHeader(file)
	if err != nil || id != threadID || parentID != "" {
		t.Fatalf("readHeader = %q, %q, %v; want thread ID %q unparented", id, parentID, err, threadID)
	}
	parsed, err := parseTranscript(file)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Session.ID != threadID || parsed.Session.WorkingDirectory != "/fork/dir" || parsed.ParentID != "" {
		t.Fatalf("parsed fork = %q in %q parented by %q; want %q in \"/fork/dir\" unparented",
			parsed.Session.ID, parsed.Session.WorkingDirectory, parsed.ParentID, threadID)
	}
}

func TestChainedForkedRolloutPrefersNewestMatchingMeta(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	middleID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	threadID := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	dir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "rollout-2026-07-10T14-11-18-"+rootID+"_"+middleID+"_"+threadID+".jsonl")
	content := `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + middleID +
		`","session_id":"` + middleID + `","cwd":"/middle/dir"}}` + "\n" +
		`{"timestamp":"2026-07-10T14:12:18Z","type":"session_meta","payload":{"id":"` + rootID +
		`","session_id":"` + rootID + `","cwd":"/fork/dir"}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed, err := parseSource(vendors.LocalReadSource, file, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if parsed.transcript.Session.WorkingDirectory != "/fork/dir" {
		t.Fatalf("working directory = %q, want newest matching meta", parsed.transcript.Session.WorkingDirectory)
	}
	if parsed.fork.forkedFromID != middleID {
		t.Fatalf("inferred fork parent = %q, want %q", parsed.fork.forkedFromID, middleID)
	}
}

func TestForkedUsageFindsUnchangedActiveParent(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	dir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "rollout-2026-07-10T14-11-18-"+rootID+".jsonl")
	fork := filepath.Join(dir, "rollout-2026-07-10T14-12-18-"+rootID+"_"+threadID+".jsonl")
	meta := func(id string) string {
		return `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + id + `","session_id":"` + id + `"}}` + "\n" +
			`{"timestamp":"2026-07-10T14:11:19Z","type":"turn_context","payload":{"model":"gpt-5"}}` + "\n"
	}
	tokens := func(input, output int) string {
		return `{"timestamp":"2026-07-10T14:11:20Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` +
			fmt.Sprint(input) + `,"output_tokens":` + fmt.Sprint(output) + `}}}}` + "\n"
	}
	if err := os.WriteFile(root, []byte(meta(rootID)+tokens(100, 10)+tokens(200, 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fork, []byte(meta(rootID)+tokens(100, 10)+tokens(200, 20)+tokens(260, 30)), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseFamilyFilesSource(vendors.LocalReadSource, home, []string{fork})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 {
		t.Fatalf("parsed sessions = %d, want 1", len(parsed))
	}
	usage := parsed[0].Session.Tokens["gpt-5"]
	if usage.InputTokens != 60 || usage.OutputTokens != 10 {
		t.Fatalf("fork tokens = %#v, want 60 input and 10 output", usage)
	}
}

func TestForkedRolloutStillRejectsUnrelatedHeaderID(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	file := writeFamilyTestRollout(t, home, rootID+"_"+threadID, "bbbbbbbb-cccc-dddd-eeee-ffffffffffff")

	_, _, err := readHeader(file)
	if !errors.Is(err, vendors.ErrInvalidData) {
		t.Fatalf("readHeader error = %v, want invalid data", err)
	}
}

func writeFamilyTestRollout(t *testing.T, home, filenameIDs, sessionID string) string {
	t.Helper()
	file := filepath.Join(home, ".codex", "sessions", "rollout-2026-07-10T14-11-18-"+filenameIDs+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + sessionID + `","session_id":"` + sessionID + `"}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}
