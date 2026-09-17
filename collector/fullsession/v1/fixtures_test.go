package fullsessionv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fixtureManifest struct {
	SchemaVersion string `json:"schemaVersion"`
	Fixtures      []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Valid  bool   `json:"valid"`
	} `json:"fixtures"`
}

func TestSessionTimestampBoundary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "fixtures", "valid", "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	record.Session.StartedAtMs = MaxSessionTimestampMs
	record.Session.LastActivityAtMs = MaxSessionTimestampMs
	if _, err := Freeze(record); err != nil {
		t.Fatalf("exact maximum rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Record){
		"started": func(record *Record) {
			record.Session.StartedAtMs = MaxSessionTimestampMs + 1
			record.Session.LastActivityAtMs = MaxSessionTimestampMs + 1
		},
		"last activity": func(record *Record) {
			record.Session.LastActivityAtMs = MaxSessionTimestampMs + 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := record
			if name == "last activity" {
				candidate.Session.StartedAtMs = MaxSessionTimestampMs
			}
			mutate(&candidate)
			if _, err := Freeze(candidate); !errors.Is(err, ErrInvalid) {
				t.Fatalf("maximum + 1 error = %v; want %v", err, ErrInvalid)
			}
		})
	}
}

func TestTimestampOutOfRangeFixtureHasConsistentRevisionHash(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "fixtures", "invalid", "timestamp-out-of-range.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	want := record.RevisionID
	record.RevisionID = ""
	preimage, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(preimage)
	if got := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("revision hash = %s; want %s", got, want)
	}
	if record.Session.LastActivityAtMs != MaxSessionTimestampMs+1 {
		t.Fatalf("last activity = %d", record.Session.LastActivityAtMs)
	}
	record.Session.LastActivityAtMs = MaxSessionTimestampMs
	if _, err := Freeze(record); err != nil {
		t.Fatalf("fixture remains invalid after repairing timestamp: %v", err)
	}
}

func TestPublishedFixtures(t *testing.T) {
	root := filepath.Join("testdata", "fixtures")
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != SchemaVersion {
		t.Fatalf("manifest schema = %q", manifest.SchemaVersion)
	}
	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		t.Run(fixture.Path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(fixture.Path)))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			if got := hex.EncodeToString(sum[:]); got != fixture.SHA256 {
				t.Fatalf("sha256 = %s", got)
			}
			_, err = Decode(data)
			if fixture.Valid && err != nil {
				t.Fatalf("valid fixture rejected: %v", err)
			}
			if !fixture.Valid && err == nil {
				t.Fatal("invalid fixture accepted")
			}
		})
	}
}

func TestCanonicalEscapingFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "fixtures", "valid", "escaping.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`"summary":"\u003ctag\u003e\u0026 line\u2028paragraph\u2029 quote=\" slash=\\ controls=\b\f\n\r\t\u0000\u0001"`)
	if !bytes.Contains(data, want) {
		t.Fatalf("escaping fixture does not contain canonical summary %q", want)
	}
}
