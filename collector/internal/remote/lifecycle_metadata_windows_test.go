package remote

import (
	"path/filepath"
	"testing"
)

func TestFileMetadataSequenceStoreInitialAcceptOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "sequence")
	if err := (&FileMetadataSequenceStore{Path: path}).Accept(4); err != nil {
		t.Fatal(err)
	}
}
