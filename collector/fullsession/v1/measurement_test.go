package fullsessionv1

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeasureReportsOnlyCountsAndSizes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "fixtures", "valid", "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Measure([]Record{record}, "fixturegen/1")
	if err != nil {
		t.Fatal(err)
	}
	if report.CorpusSize != 1 || report.MaximumBytes != len(data) || report.TotalChangeBodies != 2 || report.MaximumChangeBytes == 0 {
		t.Fatalf("measurement = %#v", report)
	}
}

func TestMeasurerMatchesBatchMeasurement(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "fixtures", "valid", "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Measure([]Record{record, record}, "fixturegen/1")
	if err != nil {
		t.Fatal(err)
	}
	measurer, err := NewMeasurer("fixturegen/1")
	if err != nil {
		t.Fatal(err)
	}
	if err := measurer.Add(record); err != nil {
		t.Fatal(err)
	}
	if err := measurer.Add(record); err != nil {
		t.Fatal(err)
	}
	got, err := measurer.Report()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("incremental measurement = %#v, want %#v", got, want)
	}
}
