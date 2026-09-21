package vendors

import (
	"context"
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

func ScanSource(source ReadSource, root string) (*SourceScan, error) {
	return ScanSourceContext(context.Background(), source, root)
}

func ScanSourceContext(ctx context.Context, source ReadSource, root string) (*SourceScan, error) {
	scan := &SourceScan{Files: []string{}, Skipped: []SkippedPath{}}
	err := walkReadSourceContext(ctx, source, root, func(path string, entry fs.DirEntry, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
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

func JSONLFilesUnderSource(source ReadSource, root string) ([]string, error) {
	return JSONLFilesUnderSourceContext(context.Background(), source, root)
}

func JSONLFilesUnderSourceContext(ctx context.Context, source ReadSource, root string) ([]string, error) {
	scan, err := ScanSourceContext(ctx, source, root)
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
