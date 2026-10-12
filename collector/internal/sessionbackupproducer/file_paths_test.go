package sessionbackupproducer

import (
	"os"
	"path/filepath"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func TestSanitizeRecordFilePathsForSupportedReaders(t *testing.T) {
	readers := []string{"claude", "codex", "cursor", "opencode"}
	cases := []struct {
		name, home, cwd, path, want string
	}{
		{"inside repository", "/Users/alice", "/Users/alice/work/repo", "/Users/alice/work/repo/src/main.go", "src/main.go"},
		{"elsewhere under home", "/Users/alice", "/Users/alice/work/repo", "/Users/alice/.config/editor.json", "~/.config/editor.json"},
		{"outside home", "/Users/alice", "/Users/alice/work/repo", "/private/tmp/claude-501/run/part-000.txt", "part-000.txt"},
		{"Windows repository", `C:\Users\Alice`, `C:\Users\Alice\work\repo`, `C:\Users\Alice\work\repo\src\main.go`, "src/main.go"},
		{"Windows home", `C:\Users\Alice`, `C:\Users\Alice\work\repo`, `C:\Users\Alice\AppData\state.json`, "~/AppData/state.json"},
		{"Windows outside home", `C:\Users\Alice`, `C:\Users\Alice\work\repo`, `D:\Temp\private\part-000.txt`, "part-000.txt"},
	}
	for _, reader := range readers {
		t.Run(reader, func(t *testing.T) {
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					record, err := fullsessionv1.Freeze(fullsessionv1.Record{
						SourceID: "local", Agent: reader, SessionID: "session",
						Session: fullsessionv1.Session{
							WorkingDirectory: test.cwd, EditedFileCount: 1, StartedAtMs: 1, LastActivityAtMs: 1,
							FileEdits: []fullsessionv1.FileEdit{{Path: test.path, Edits: 1,
								Changes: []fullsessionv1.FileChange{{Kind: "content", Text: "body", Operation: "Write"}},
							}},
						},
					})
					if err != nil {
						t.Fatal(err)
					}
					before := record.RevisionID
					records := []fullsessionv1.Record{record}
					if err := sanitizeRecordFilePaths(records, test.home); err != nil {
						t.Fatal(err)
					}
					if got := records[0].Session.FileEdits[0].Path; got != test.want {
						t.Fatalf("sanitized path = %q, want %q", got, test.want)
					}
					if records[0].RevisionID == before {
						t.Fatal("sanitizing the path did not update the revision")
					}
					if err := fullsessionv1.Validate(records[0]); err != nil {
						t.Fatalf("sanitized record is invalid: %v", err)
					}
				})
			}
		})
	}
}

func readProcessedRecord(t *testing.T, spool string, prepared *Prepared, memberID string) fullsessionv1.Record {
	t.Helper()
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.MemberID != memberID || artifact.Kind != sessionbackupv1.KindParsedSessionRecord {
			continue
		}
		data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		record, err := fullsessionv1.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	t.Fatalf("processed record for member %q is missing", memberID)
	return fullsessionv1.Record{}
}

func TestSanitizeRecordFilePathsRefreezesRevision(t *testing.T) {
	record, err := fullsessionv1.Freeze(fullsessionv1.Record{
		SourceID: "local", Agent: "claude", SessionID: "session",
		Session: fullsessionv1.Session{
			WorkingDirectory: "/Users/alice/work/repo", EditedFileCount: 1, StartedAtMs: 1, LastActivityAtMs: 1,
			FileEdits: []fullsessionv1.FileEdit{{Path: "/Users/alice/work/repo/src/main.go", Edits: 1}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := record.RevisionID
	records := []fullsessionv1.Record{record}
	if err := sanitizeRecordFilePaths(records, "/Users/alice"); err != nil {
		t.Fatal(err)
	}
	if records[0].RevisionID == before {
		t.Fatal("sanitizing the path did not update the revision")
	}
	if got := records[0].Session.FileEdits[0].Path; got != "src/main.go" {
		t.Fatalf("path = %q", got)
	}
}
