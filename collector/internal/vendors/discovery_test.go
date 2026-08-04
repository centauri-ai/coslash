package vendors

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFindsJSONLAndDistinguishesMissingRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	wanted := filepath.Join(nested, "session.jsonl")
	if err := os.WriteFile(wanted, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "ignore.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	scan, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Files) != 1 || scan.Files[0] != wanted || scan.RootMissing {
		t.Fatalf("unexpected scan: %#v", scan)
	}

	missing, err := Scan(filepath.Join(root, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if !missing.RootMissing || len(missing.Files) != 0 {
		t.Fatalf("unexpected missing-root scan: %#v", missing)
	}
}

func TestScanRecordsUnreadablePaths(t *testing.T) {
	root := t.TempDir()
	unreadable := filepath.Join(root, "unreadable")
	if err := os.Mkdir(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unreadable, "session.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o700) })

	scan, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Skipped) == 0 {
		t.Skip("filesystem permissions are not enforced for this user")
	}
	if scan.SkippedTotal != 1 || scan.Skipped[0].Path != unreadable {
		t.Fatalf("unexpected skipped paths: %#v", scan)
	}
}
