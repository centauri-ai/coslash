package remote

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

const fakeHome = "/home/testuser"

type fakeEntry struct {
	isDir   bool
	content []byte
	modTime time.Time
	symlink bool
}

type fakeFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (info fakeFileInfo) Name() string       { return info.name }
func (info fakeFileInfo) Size() int64        { return info.size }
func (info fakeFileInfo) Mode() fs.FileMode  { return info.mode }
func (info fakeFileInfo) ModTime() time.Time { return info.modTime }
func (info fakeFileInfo) IsDir() bool        { return info.isDir }
func (info fakeFileInfo) Sys() any           { return nil }

// fakeCounts tracks how many times each SFTP-shaped operation ran, so tests
// can prove a cache hit avoided a round trip instead of merely avoiding a
// wrong answer.
type fakeCounts struct {
	mu                             sync.Mutex
	LStat, RealPath, ReadDir, Open int
}

type fakeCountsSnapshot struct {
	LStat, RealPath, ReadDir, Open int
}

func (c *fakeCounts) snapshot() fakeCountsSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fakeCountsSnapshot{LStat: c.LStat, RealPath: c.RealPath, ReadDir: c.ReadDir, Open: c.Open}
}

// fakeFS is a minimal in-memory stand-in for an SFTP server: enough to drive
// remote.Source without a real network round trip.
type fakeFS struct {
	mu     sync.Mutex
	files  map[string]*fakeEntry
	counts fakeCounts
	opens  map[string]int
	onOpen func(string)
}

func newFakeFS() *fakeFS {
	return &fakeFS{files: map[string]*fakeEntry{fakeHome: {isDir: true}}, opens: map[string]int{}}
}

// openCounts returns a snapshot of how many times each path was opened, so a
// test can assert precisely which files a second refresh reopened.
func (f *fakeFS) openCounts() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make(map[string]int, len(f.opens))
	for path, count := range f.opens {
		result[path] = count
	}
	return result
}

func (f *fakeFS) mkdirAll(dir string) {
	for dir != "/" && dir != "." && dir != "" {
		if _, ok := f.files[dir]; !ok {
			f.files[dir] = &fakeEntry{isDir: true, modTime: time.Unix(0, 0)}
		}
		dir = path.Dir(dir)
	}
}

func (f *fakeFS) writeFile(p string, content string, modTime time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mkdirAll(path.Dir(p))
	f.files[p] = &fakeEntry{content: []byte(content), modTime: modTime}
}

func (f *fakeFS) symlinkFile(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mkdirAll(path.Dir(p))
	f.files[p] = &fakeEntry{symlink: true, modTime: time.Unix(0, 0)}
}

func (f *fakeFS) infoFor(p string, entry *fakeEntry) os.FileInfo {
	mode := fs.FileMode(0o644)
	if entry.isDir {
		mode = fs.ModeDir | 0o755
	}
	if entry.symlink {
		mode = fs.ModeSymlink | 0o777
	}
	return fakeFileInfo{
		name: path.Base(p), size: int64(len(entry.content)), mode: mode,
		modTime: entry.modTime, isDir: entry.isDir,
	}
}

func notExist(p string) error {
	return &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
}

func (f *fakeFS) ops() sftpOperations {
	return sftpOperations{
		realPath: func(p string) (string, error) {
			f.counts.mu.Lock()
			f.counts.RealPath++
			f.counts.mu.Unlock()
			if p == "." {
				return fakeHome, nil
			}
			return path.Clean(p), nil
		},
		lstat: func(p string) (os.FileInfo, error) {
			f.counts.mu.Lock()
			f.counts.LStat++
			f.counts.mu.Unlock()
			f.mu.Lock()
			defer f.mu.Unlock()
			entry, ok := f.files[path.Clean(p)]
			if !ok {
				return nil, notExist(p)
			}
			return f.infoFor(p, entry), nil
		},
		readDir: func(p string) ([]os.FileInfo, error) {
			f.counts.mu.Lock()
			f.counts.ReadDir++
			f.counts.mu.Unlock()
			f.mu.Lock()
			defer f.mu.Unlock()
			clean := path.Clean(p)
			prefix := clean + "/"
			seen := map[string]bool{}
			var result []os.FileInfo
			for full, entry := range f.files {
				if !strings.HasPrefix(full, prefix) {
					continue
				}
				rest := strings.TrimPrefix(full, prefix)
				if rest == "" || strings.Contains(rest, "/") {
					continue
				}
				if seen[rest] {
					continue
				}
				seen[rest] = true
				result = append(result, f.infoFor(full, entry))
			}
			return result, nil
		},
		open: func(p string) (io.ReadCloser, error) {
			f.counts.mu.Lock()
			f.counts.Open++
			f.counts.mu.Unlock()
			if f.onOpen != nil {
				f.onOpen(path.Clean(p))
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.opens[path.Clean(p)]++
			entry, ok := f.files[path.Clean(p)]
			if !ok || entry.isDir {
				return nil, notExist(p)
			}
			return io.NopCloser(bytes.NewReader(entry.content)), nil
		},
	}
}

func newFakeSource(f *fakeFS, limits Limits) *Source {
	source, err := newSource(f.ops(), limits)
	if err != nil {
		panic(err)
	}
	return source
}
