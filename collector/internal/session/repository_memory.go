package session

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	maxRememberedRepositories = 10_000
	maxRepositoryMemoryBytes  = 4 << 20
)

// repositoryMemory maps a working directory to the remote Git last reported
// for it, so a session keeps its repository once its worktree is removed or
// its folder cannot be probed.
var repositoryMemory struct {
	sync.Mutex
	path    string
	entries map[string]string
}

// UseRepositoryMemory loads the file that remembers each working directory's
// remote. An empty path turns the memory off, which is the default.
func UseRepositoryMemory(path string) {
	entries := map[string]string{}
	if path != "" {
		if file, err := os.Open(path); err == nil {
			data, readErr := io.ReadAll(io.LimitReader(file, maxRepositoryMemoryBytes+1))
			file.Close()
			if readErr != nil || len(data) > maxRepositoryMemoryBytes || json.Unmarshal(data, &entries) != nil {
				entries = map[string]string{}
			}
		}
	}
	repositoryMemory.Lock()
	defer repositoryMemory.Unlock()
	repositoryMemory.path, repositoryMemory.entries = path, entries
}

// RememberedRepository returns the remote last recorded for key, or "".
func RememberedRepository(key string) string {
	repositoryMemory.Lock()
	defer repositoryMemory.Unlock()
	return repositoryMemory.entries[key]
}

// RememberRepository records a remote Git reported for key. The file is
// written only when the entry is new or changed.
func RememberRepository(key, repository string) {
	repositoryMemory.Lock()
	defer repositoryMemory.Unlock()
	entries := repositoryMemory.entries
	previous, known := entries[key]
	if repositoryMemory.path == "" || key == "" || repository == "" || previous == repository ||
		(!known && len(entries) >= maxRememberedRepositories) {
		return
	}
	entries[key] = repository
	if data, err := json.Marshal(entries); err == nil && len(data) <= maxRepositoryMemoryBytes {
		_ = writeFileAtomic(repositoryMemory.path, data)
	}
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".repositories-*")
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Rename(file.Name(), path)
	}
	if writeErr != nil {
		os.Remove(file.Name())
	}
	return writeErr
}
