package vendors

import (
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"
)

type mapReadSource struct {
	fsys fstest.MapFS
}

func (source mapReadSource) Open(path string) (io.ReadCloser, error) {
	return source.fsys.Open(path)
}

func (source mapReadSource) ReadDir(path string) ([]fs.DirEntry, error) {
	return fs.ReadDir(source.fsys, path)
}

func (source mapReadSource) Stat(path string) (fs.FileInfo, error) {
	return fs.Stat(source.fsys, path)
}

func TestScanSourcePreservesSortedJSONLDiscovery(t *testing.T) {
	source := mapReadSource{fsys: fstest.MapFS{
		"root/b.jsonl": {Data: []byte("{}\n")},
		"root/a.jsonl": {Data: []byte("{}\n")},
		"root/a.txt":   {Data: []byte("ignored")},
	}}
	scan, err := ScanSource(source, "root")
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Files) != 2 || scan.Files[0] != "root/a.jsonl" || scan.Files[1] != "root/b.jsonl" {
		t.Fatalf("unexpected files: %#v", scan.Files)
	}
}

func TestScanSourceReportsMissingRoot(t *testing.T) {
	scan, err := ScanSource(mapReadSource{fsys: fstest.MapFS{}}, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if !scan.RootMissing || len(scan.Files) != 0 {
		t.Fatalf("unexpected scan: %#v", scan)
	}
}

func TestParseJSONLSourceAcceptsTornFinalRecord(t *testing.T) {
	type row struct {
		Value int `json:"value"`
	}
	source := mapReadSource{fsys: fstest.MapFS{
		"rows.jsonl": {Data: []byte("{\"value\":1}\n{\"value\":")},
	}}
	rows, err := ParseJSONLSource[row](source, "rows.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Value != 1 {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestParseJSONLSourceRejectsMalformedRecord(t *testing.T) {
	source := mapReadSource{fsys: fstest.MapFS{
		"rows.jsonl": {Data: []byte("{not-json}\n")},
	}}
	if _, err := ParseJSONLSource[map[string]any](source, "rows.jsonl"); err == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestLimitNewestSourceFilesIsDeterministic(t *testing.T) {
	source := mapReadSource{fsys: fstest.MapFS{
		"root/old.jsonl": {Data: []byte("{}\n"), ModTime: time.Unix(1, 0)},
		"root/b.jsonl":   {Data: []byte("{}\n"), ModTime: time.Unix(2, 0)},
		"root/a.jsonl":   {Data: []byte("{}\n"), ModTime: time.Unix(2, 0)},
	}}
	files := []string{"root/old.jsonl", "root/b.jsonl", "root/a.jsonl"}
	selected, truncated := LimitNewestSourceFiles(source, files, 2)
	if !truncated {
		t.Fatal("expected truncation")
	}
	want := []string{"root/b.jsonl", "root/a.jsonl"}
	if len(selected) != len(want) || selected[0] != want[0] || selected[1] != want[1] {
		t.Fatalf("selected = %#v, want %#v", selected, want)
	}
}
