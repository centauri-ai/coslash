package vendors

import (
	"context"
	"errors"
	"fmt"
	"io"
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

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(destination)
}

// ParseFiles parses concurrently while preserving source order.
func ParseFiles[T any](
	files []string,
	parse func(string) (*T, error),
) []*T {
	parsed, _ := ParseFilesContext(context.Background(), files, func(_ context.Context, path string) (*T, error) {
		return parse(path)
	})
	return parsed
}

// ParseFilesContext parses concurrently while preserving source order and
// waits for started workers before returning from cancellation.
func ParseFilesContext[T any](
	ctx context.Context,
	files []string,
	parse func(context.Context, string) (*T, error),
) ([]*T, error) {
	results := make([]*T, len(files))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(maxParseWorkers, len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				file := files[index]
				parsed, err := parse(ctx, file)
				if err != nil {
					if ctx.Err() == nil {
						log.Printf("%s: transcript parse failed; skipping: %v", file, err)
					}
					continue
				}
				results[index] = parsed
			}
		}()
	}
	for index := range files {
		if err := ctx.Err(); err != nil {
			close(jobs)
			wg.Wait()
			return nil, err
		}
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	parsed := make([]*T, 0, len(files))
	for _, result := range results {
		if result != nil {
			parsed = append(parsed, result)
		}
	}
	return parsed, nil
}

// ParseSourceFiles parses concurrently while preserving source order.
func ParseSourceFiles[T any](
	source ReadSource,
	files []string,
	parse func(ReadSource, string) (*T, error),
) []*T {
	return ParseFiles(files, func(path string) (*T, error) { return parse(source, path) })
}

func ParseSourceFilesContext[T any](
	ctx context.Context,
	source ReadSource,
	files []string,
	parse func(context.Context, ReadSource, string) (*T, error),
) ([]*T, error) {
	return ParseFilesContext(ctx, files, func(ctx context.Context, path string) (*T, error) {
		return parse(ctx, source, path)
	})
}

// FileFailure names one file that failed strict parsing, so a caller can map
// the failure back to the family that file belongs to instead of discarding
// every other result.
type FileFailure struct {
	Path string
	Err  error
}

// ParseSourceFilesStrict returns every main-file failure to the remote refresh
// owner alongside every file that parsed successfully. Callers that need
// per-family isolation must exclude a failed file's whole family from the
// returned results themselves; this function does not know about families.
func ParseSourceFilesStrict[T any](
	source ReadSource,
	files []string,
	parse func(ReadSource, string) (*T, error),
) ([]*T, []FileFailure, error) {
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
				failures[index] = err
				return
			}
			results[index] = parsed
		}()
	}
	wg.Wait()

	parsed := make([]*T, 0, len(files))
	var fileFailures []FileFailure
	var joined []error
	for index, result := range results {
		if failures[index] != nil {
			fileFailures = append(fileFailures, FileFailure{Path: files[index], Err: failures[index]})
			joined = append(joined, fmt.Errorf("%s: %w", files[index], failures[index]))
			continue
		}
		if result != nil {
			parsed = append(parsed, result)
		}
	}
	if len(joined) > 0 {
		return parsed, fileFailures, errors.Join(joined...)
	}
	return parsed, nil, nil
}

func ParseSourceFilesStrictContext[T any](
	ctx context.Context,
	source ReadSource,
	files []string,
	parse func(context.Context, ReadSource, string) (*T, error),
) ([]*T, []FileFailure, error) {
	results := make([]*T, len(files))
	failures := make([]error, len(files))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(maxParseWorkers, len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				results[index], failures[index] = parse(ctx, source, files[index])
			}
		}()
	}
	for index := range files {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	parsed := make([]*T, 0, len(files))
	var fileFailures []FileFailure
	var joined []error
	for index, result := range results {
		if failures[index] != nil {
			fileFailures = append(fileFailures, FileFailure{Path: files[index], Err: failures[index]})
			joined = append(joined, fmt.Errorf("%s: %w", files[index], failures[index]))
		} else if result != nil {
			parsed = append(parsed, result)
		}
	}
	return parsed, fileFailures, errors.Join(joined...)
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

func NewIndexedParser(
	files []string,
	idFromPath func(string) string,
	parse func(string) (*ParsedSession, error),
) func(string) (*ParsedSession, error) {
	byID := make(map[string]string, len(files))
	for _, file := range files {
		id := idFromPath(file)
		if _, exists := byID[id]; id != "" && !exists {
			byID[id] = file
		}
	}
	return func(id string) (*ParsedSession, error) {
		file, ok := byID[id]
		if !ok {
			return nil, nil
		}
		return parse(file)
	}
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
