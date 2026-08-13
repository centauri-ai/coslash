package vendors

import (
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
