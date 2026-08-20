package remoteviewv1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
				t.Fatalf("sha256 = %s; want %s", got, fixture.SHA256)
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
