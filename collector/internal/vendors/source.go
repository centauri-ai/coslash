package vendors

import (
	"errors"
	"fmt"
	"log"
	"sync"
)

const maxParseWorkers = 8

type SourceHealth struct {
	Agent        string
	Root         string
	Entries      int
	Sessions     int
	Missing      bool
	Skipped      []SkippedPath
	SkippedTotal int
	Err          error
}

// ParseFiles parses concurrently while preserving source order.
func ParseFiles[T any](
	files []string,
	parse func(string) (*T, error),
) []*T {
	results := make([]*T, len(files))
	workers := make(chan struct{}, maxParseWorkers)
	var wg sync.WaitGroup
	for index, file := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workers <- struct{}{}
			defer func() { <-workers }()
			parsed, err := parse(file)
			if err != nil {
				log.Printf("%s: transcript parse failed; skipping: %v", file, err)
				return
			}
			results[index] = parsed
		}()
	}
	wg.Wait()

	parsed := make([]*T, 0, len(files))
	for _, result := range results {
		if result != nil {
			parsed = append(parsed, result)
		}
	}
	return parsed
}

// ParseSourceFiles parses concurrently while preserving source order.
func ParseSourceFiles[T any](
	source ReadSource,
	files []string,
	parse func(ReadSource, string) (*T, error),
) []*T {
	return ParseFiles(files, func(path string) (*T, error) { return parse(source, path) })
}

// ParseSourceFilesStrict returns every main-file failure to the remote refresh owner.
func ParseSourceFilesStrict[T any](
	source ReadSource,
	files []string,
	parse func(ReadSource, string) (*T, error),
) ([]*T, error) {
	results := make([]*T, len(files))
	failures := make([]error, len(files))
	workers := make(chan struct{}, maxParseWorkers)
	var wg sync.WaitGroup
	for index, file := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workers <- struct{}{}
			defer func() { <-workers }()
			parsed, err := parse(source, file)
			if err != nil {
				failures[index] = fmt.Errorf("%s: %w", file, err)
				return
			}
			results[index] = parsed
		}()
	}
	wg.Wait()

	parsed := make([]*T, 0, len(files))
	var joined []error
	for index, result := range results {
		if failures[index] != nil {
			joined = append(joined, failures[index])
			continue
		}
		if result != nil {
			parsed = append(parsed, result)
		}
	}
	if len(joined) > 0 {
		return nil, errors.Join(joined...)
	}
	return parsed, nil
}

func FindAndParse(
	files []string,
	id string,
	idFromPath func(string) string,
	parse func(string) (*ParsedSession, error),
) (*ParsedSession, error) {
	for _, file := range files {
		if idFromPath(file) == id {
			return parse(file)
		}
	}
	return nil, nil
}

func FileSourceHealth(
	agent string,
	root string,
	scan *SourceScan,
	isRoot func(string) (bool, error),
) SourceHealth {
	sessions := 0
	for _, file := range scan.Files {
		isRootSession, err := isRoot(file)
		if err != nil {
			scan.RecordSkipped(file, err)
			continue
		}
		if isRootSession {
			sessions++
		}
	}
	return SourceHealth{
		Agent:        agent,
		Root:         root,
		Entries:      len(scan.Files),
		Sessions:     sessions,
		Missing:      scan.RootMissing,
		Skipped:      scan.Skipped,
		SkippedTotal: max(scan.SkippedTotal, len(scan.Skipped)),
	}
}
