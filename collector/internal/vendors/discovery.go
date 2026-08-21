package vendors

import (
	"errors"
	"io/fs"
	"log"
	"strings"
)

const maxRecordedSkippedPaths = 10

type SkippedPath struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type SourceScan struct {
	Files        []string
	Skipped      []SkippedPath
	SkippedTotal int
	RootMissing  bool
}

func (scan *SourceScan) RecordSkipped(path string, err error) {
	scan.SkippedTotal++
	if len(scan.Skipped) < maxRecordedSkippedPaths {
		scan.Skipped = append(scan.Skipped, SkippedPath{Path: path, Error: err.Error()})
	}
}

func Scan(root string) (*SourceScan, error) {
	return ScanSource(LocalReadSource, root)
}

func ScanSource(source ReadSource, root string) (*SourceScan, error) {
	scan := &SourceScan{Files: []string{}, Skipped: []SkippedPath{}}
	err := walkReadSource(source, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				if errors.Is(err, fs.ErrNotExist) {
					scan.RootMissing = true
					return nil
				}
				return err
			}
			scan.RecordSkipped(path, err)
			return nil
		}
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
			scan.Files = append(scan.Files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return scan, nil
}

// JSONLFilesUnder scans the collection path and logs entries skipped during discovery.
func JSONLFilesUnder(root string) ([]string, error) {
	return JSONLFilesUnderSource(LocalReadSource, root)
}

func JSONLFilesUnderSource(source ReadSource, root string) ([]string, error) {
	scan, err := ScanSource(source, root)
	if err != nil {
		return nil, err
	}
	for _, skipped := range scan.Skipped {
		log.Printf("transcript scan %q: %s; skipping", skipped.Path, skipped.Error)
	}
	if scan.SkippedTotal > len(scan.Skipped) {
		log.Printf("transcript scan: %d additional paths skipped", scan.SkippedTotal-len(scan.Skipped))
	}
	return scan.Files, nil
}
