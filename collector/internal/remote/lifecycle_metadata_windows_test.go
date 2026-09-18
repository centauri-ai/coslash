package remote

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestFileMetadataSequenceStoreInitialAcceptOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "sequence")
	if err := (&FileMetadataSequenceStore{Path: path}).Accept(4); err != nil {
		t.Fatal(err)
	}
	assertPrivateWindowsACL(t, filepath.Dir(path), true)
	assertPrivateWindowsACL(t, path, false)
}

func TestFileMetadataSequenceStoreRepairsLegacyBroadACLs(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(directory, "sequence")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setWindowsDACL(t, directory, "D:(A;OICI;FA;;;WD)", true)
	setWindowsDACL(t, path, "D:(A;;FA;;;WD)", false)

	if err := (&FileMetadataSequenceStore{Path: path}).Accept(4); err != nil {
		t.Fatal(err)
	}
	assertPrivateWindowsACL(t, directory, true)
	assertPrivateWindowsACL(t, path, false)
}

func TestFileMetadataSequenceStoreRejectsHardLinks(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(directory, "sequence")
	store := &FileMetadataSequenceStore{Path: path}
	if err := store.Accept(4); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(directory, "sequence-copy")); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(4); err == nil || !strings.Contains(err.Error(), "must not be hard linked") {
		t.Fatalf("hard-linked sequence error = %v", err)
	}
}

func TestFileMetadataSequenceStoreRejectsReparseDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-state")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	err := (&FileMetadataSequenceStore{Path: filepath.Join(link, "sequence")}).Accept(4)
	if err == nil || !strings.Contains(err.Error(), "must not be a reparse point") {
		t.Fatalf("reparse directory error = %v", err)
	}
}

func TestFileMetadataSequenceStoreSupportsLongUnicodePath(t *testing.T) {
	directory := t.TempDir()
	for index := 0; len(utf16.Encode([]rune(directory))) <= 300; index++ {
		directory = filepath.Join(directory, fmt.Sprintf("元数据-%02d-%s", index, strings.Repeat("长", 24)))
	}
	path := filepath.Join(directory, "sequence")
	store := &FileMetadataSequenceStore{Path: path}
	if err := store.Accept(4); err != nil {
		t.Fatal(err)
	}
	if err := (&FileMetadataSequenceStore{Path: path}).Accept(4); err != nil {
		t.Fatal(err)
	}
	assertPrivateWindowsACL(t, directory, true)
	assertPrivateWindowsACL(t, path, false)
	if err := store.Accept(3); !errors.Is(err, ErrHelperMetadataRollback) {
		t.Fatalf("rollback error = %v", err)
	}
}
