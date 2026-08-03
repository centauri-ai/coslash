package collector

import (
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

type ListOptions struct {
	Since int64
}

type fileVersion struct {
	size  int64
	mtime int64
}

type transcriptFile struct {
	path    string
	version fileVersion
}

type parsedCacheEntry struct {
	agent   string
	version fileVersion
	parsed  *vendors.ParsedTranscript
}

type headerCacheEntry struct {
	version fileVersion
	header  codex.Header
	err     error
}

type collectionStats struct {
	hits   atomic.Int64
	misses atomic.Int64
}

type Collector struct {
	operationMu sync.Mutex
	cacheMu     sync.Mutex
	parsed      map[string]parsedCacheEntry
	headers     map[string]headerCacheEntry
}

func New() *Collector {
	return &Collector{
		parsed:  map[string]parsedCacheEntry{},
		headers: map[string]headerCacheEntry{},
	}
}

func (c *Collector) List(options ListOptions) ([]*session.Session, error) {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()

	started := time.Now()
	stats := &collectionStats{}
	sessions, err := c.list(options, stats)
	if err != nil {
		return nil, err
	}
	log.Printf(
		"list sessions: %d window_since_ms=%d cache_hits=%d cache_misses=%d duration=%s",
		len(sessions), options.Since, stats.hits.Load(), stats.misses.Load(), time.Since(started),
	)
	return sessions, nil
}

func statTranscriptFiles(paths []string) []transcriptFile {
	files := make([]transcriptFile, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			log.Printf("stat transcript %q: %v; skipping", path, err)
			continue
		}
		files = append(files, transcriptFile{
			path: path,
			version: fileVersion{
				size:  info.Size(),
				mtime: info.ModTime().UnixNano(),
			},
		})
	}
	return files
}

func (c *Collector) parseCached(
	source vendorSource,
	file transcriptFile,
	stats *collectionStats,
) (*vendors.ParsedTranscript, error) {
	c.cacheMu.Lock()
	entry, ok := c.parsed[file.path]
	c.cacheMu.Unlock()
	if ok && entry.agent == source.name && entry.version == file.version {
		stats.hits.Add(1)
		return cloneParsed(entry.parsed), nil
	}

	parsed, err := source.parse(file.path)
	if err != nil {
		return nil, err
	}
	stats.misses.Add(1)
	c.cacheMu.Lock()
	c.parsed[file.path] = parsedCacheEntry{
		agent: source.name, version: file.version, parsed: parsed,
	}
	c.cacheMu.Unlock()
	return cloneParsed(parsed), nil
}

func (c *Collector) codexHeader(
	file transcriptFile,
	stats *collectionStats,
) (codex.Header, error) {
	c.cacheMu.Lock()
	entry, ok := c.headers[file.path]
	c.cacheMu.Unlock()
	if ok && entry.version == file.version {
		stats.hits.Add(1)
		return entry.header, entry.err
	}
	header, err := codex.ReadHeader(file.path)
	stats.misses.Add(1)
	c.cacheMu.Lock()
	c.headers[file.path] = headerCacheEntry{version: file.version, header: header, err: err}
	c.cacheMu.Unlock()
	return header, err
}

func (c *Collector) selectCodexFiles(
	files []transcriptFile,
	metadata *vendors.SessionMetadata,
	since int64,
	stats *collectionStats,
) []transcriptFile {
	byID := make(map[string]transcriptFile, len(files))
	children := map[string][]string{}
	included := map[string]struct{}{}
	visited := map[string]struct{}{}
	queue := []string{}
	for _, file := range files {
		header, err := c.codexHeader(file, stats)
		if err != nil {
			// Unknown files stay in the parse set: correctness wins over the
			// optimization when the cheap header contract is not available.
			included[file.path] = struct{}{}
			// The filename still gives us a stable identity. Walking from it
			// ensures a malformed parent header cannot hide readable children.
			if id := codex.SessionIDFromRollout(file.path); id != "" {
				byID[id] = file
				queue = append(queue, id)
			}
			continue
		}
		byID[header.ID] = file
		if header.ParentID != "" {
			children[header.ParentID] = append(children[header.ParentID], header.ID)
			continue
		}
		_, live := metadata.Live[header.ID]
		if live || file.version.mtime/1_000_000 >= since {
			queue = append(queue, header.ID)
		}
	}

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, exists := visited[id]; exists {
			continue
		}
		visited[id] = struct{}{}
		file, ok := byID[id]
		if !ok {
			continue
		}
		included[file.path] = struct{}{}
		queue = append(queue, children[id]...)
	}

	selected := make([]transcriptFile, 0, len(included))
	for _, file := range files {
		if _, ok := included[file.path]; ok {
			selected = append(selected, file)
		}
	}
	return selected
}

func (c *Collector) evictMissing(agent string, files []transcriptFile) {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		seen[file.path] = struct{}{}
	}
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	for path, entry := range c.parsed {
		if entry.agent == agent {
			if _, ok := seen[path]; !ok {
				delete(c.parsed, path)
			}
		}
	}
	if agent == vendors.AgentCodex {
		for path := range c.headers {
			if _, ok := seen[path]; !ok {
				delete(c.headers, path)
			}
		}
	}
}

func cloneParsed(original *vendors.ParsedTranscript) *vendors.ParsedTranscript {
	if original == nil {
		return nil
	}
	cloned := *original
	cloned.SpawnTurns = cloneMap(original.SpawnTurns)
	cloned.Completed = cloneMap(original.Completed)
	cloned.Commands = cloneSlice(original.Commands)
	if original.Session == nil {
		return &cloned
	}
	s := *original.Session
	s.Tokens = cloneMap(original.Session.Tokens)
	s.Subagents = cloneSlice(original.Session.Subagents)
	for index := range s.Subagents {
		s.Subagents[index].Commands = cloneSlice(s.Subagents[index].Commands)
		s.Subagents[index].Tokens = cloneMap(s.Subagents[index].Tokens)
	}
	s.Commands = cloneSlice(original.Session.Commands)
	s.Commits = cloneSlice(original.Session.Commits)
	s.Todos = cloneSlice(original.Session.Todos)
	s.Digest = cloneSlice(original.Session.Digest)
	s.FileEdits = cloneSlice(original.Session.FileEdits)
	cloned.Session = &s
	return &cloned
}

func cloneSlice[T any](original []T) []T {
	if original == nil {
		return nil
	}
	cloned := make([]T, len(original))
	copy(cloned, original)
	return cloned
}

func cloneMap[K comparable, V any](original map[K]V) map[K]V {
	if original == nil {
		return nil
	}
	cloned := make(map[K]V, len(original))
	for key, value := range original {
		cloned[key] = value
	}
	return cloned
}
