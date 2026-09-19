package codex

import (
	"errors"
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

func TestForkedRolloutUsesThreadIDDespiteRootSessionID(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	firstForkID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	secondForkID := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	root := writeFamilyTestRollout(t, home, rootID, rootID, "", "")
	firstFork := writeFamilyTestRollout(t, home, rootID+"_"+firstForkID, rootID, rootID, "")
	secondFork := writeFamilyTestRollout(t, home, rootID+"_"+secondForkID, rootID, firstForkID, "")

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

func TestDesktopForkedRolloutWithoutHistoryBaseUsesThreadID(t *testing.T) {
	for _, originator := range []string{"Codex Desktop", "codex_work_desktop"} {
		t.Run(originator, func(t *testing.T) {
			home := t.TempDir()
			rootID := "11111111-2222-3333-4444-555555555555"
			threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
			extra := `,"source":"vscode","originator":"` + originator + `","history_mode":"paginated","thread_source":"user"`
			file := writeFamilyTestRollout(t, home, rootID+"_"+threadID, rootID, "", extra)

			id, parentID, err := readHeader(file)
			if err != nil || id != threadID || parentID != "" {
				t.Fatalf("readHeader = %q, %q, %v; want thread ID %q", id, parentID, err, threadID)
			}
		})
	}
}

func TestForkedRolloutWithoutHistoryOrDesktopMetadataIsInvalid(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	file := writeFamilyTestRollout(t, home, rootID+"_"+threadID, rootID, "", "")

	_, _, err := readHeader(file)
	if !errors.Is(err, vendors.ErrInvalidData) {
		t.Fatalf("readHeader error = %v, want invalid data", err)
	}
}

func TestForkedRolloutStillRejectsUnrelatedHeaderID(t *testing.T) {
	home := t.TempDir()
	rootID := "11111111-2222-3333-4444-555555555555"
	threadID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	file := writeFamilyTestRollout(t, home, rootID+"_"+threadID, "bbbbbbbb-cccc-dddd-eeee-ffffffffffff", rootID, "")

	_, _, err := readHeader(file)
	if !errors.Is(err, vendors.ErrInvalidData) {
		t.Fatalf("readHeader error = %v, want invalid data", err)
	}
}

func writeFamilyTestRollout(t *testing.T, home, filenameIDs, sessionID, historyBaseID, extra string) string {
	t.Helper()
	file := filepath.Join(home, ".codex", "sessions", "rollout-2026-07-10T14-11-18-"+filenameIDs+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	historyBase := ""
	if historyBaseID != "" {
		historyBase = `,"history_base":{"thread_id":"` + historyBaseID + `","end_ordinal_exclusive":1,"end_byte_offset":1}`
	}
	content := `{"timestamp":"2026-07-10T14:11:18Z","type":"session_meta","payload":{"id":"` + sessionID + `","session_id":"` + sessionID + `"` + historyBase + extra + `}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}
