package fullsessionexport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

func TestMarshalMatchesPinnedS01FixtureIdentity(t *testing.T) {
	recordBytes, err := os.ReadFile(filepath.Join("..", "..", "fullsession", "v1", "testdata", "fixtures", "valid", "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := fullsessionv1.Decode(recordBytes)
	if err != nil {
		t.Fatal(err)
	}
	payload, gotRecord, hash, err := Marshal(record, Repository{Canonical: "github.com/centauri-ai/coslash"})
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(recordBytes)
	wantHash := "sha256:" + hex.EncodeToString(wantSum[:])
	if string(gotRecord) != string(recordBytes) || len(recordBytes) != 2318 || hash != wantHash ||
		hash != "sha256:10660c3b6a01cde4838b8dddcaff29ce1d38bd67adfca87447e0b45743d04fb2" {
		t.Fatalf("record bytes=%d hash=%q", len(gotRecord), hash)
	}
	if len(payload) != 2620 {
		t.Fatalf("payload bytes = %d", len(payload))
	}
	s01, err := os.ReadFile(filepath.Join("testdata", "s01-upload-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, s01) {
		t.Fatal("canonical client envelope differs from the pinned S01 request fixture")
	}
}

func TestMarshalRejectsMissingOrUntrimmedRepository(t *testing.T) {
	for _, repository := range []string{"", " github.com/example/repo"} {
		if _, _, _, err := Marshal(fullsessionv1.Record{}, Repository{Canonical: repository}); err == nil {
			t.Fatalf("repository %q was accepted", repository)
		}
	}
}
