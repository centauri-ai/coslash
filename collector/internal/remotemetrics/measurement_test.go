package remotemetrics

import (
	"os"
	"testing"
)

func TestFixtureManifestIsPrivacySafeAndMeasurable(t *testing.T) {
	file, err := os.Open("testdata/fixture-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	manifest, err := Read(file)
	if err != nil {
		t.Fatal(err)
	}
	totals, err := Summarize(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if totals.Observations != 16 || totals.BodyBytes == 0 || totals.ResponseBytes == 0 {
		t.Fatalf("totals = %#v", totals)
	}
}
