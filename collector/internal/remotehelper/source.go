package remotehelper

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
)

// homeAllowlist is the helper's fixed read allowlist, relative to the SSH user's
// home. It matches the SFTP allowlist so both transports read the same files.
var homeAllowlist = []struct {
	relative string
	tree     bool
}{
	{relative: ".claude/projects", tree: true},
	{relative: ".claude/sessions", tree: true},
	{relative: ".claude/jobs", tree: true},
	{relative: ".codex/sessions", tree: true},
	{relative: ".codex/archived_sessions", tree: true},
	{relative: ".codex/session_index.jsonl"},
}

// Source is the only filesystem access collection has. Every operation resolves
// beneath one open home directory handle, so a path component swapped for a
// symlink mid-traversal cannot redirect a read outside the home tree. Names
// arriving from the vendor parsers are absolute host paths; names arriving from
// a Mac request are never paths at all and never reach this type.
type Source struct {
	root   *os.Root
	home   string
	limits Limits

	entries atomic.Int64
}

// OpenSource opens the home directory once and derives every later read from
// that handle.
func OpenSource(home string, limits Limits) (*Source, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("%w: home must be absolute", ErrPathDenied)
	}
	home = filepath.Clean(home)
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, fmt.Errorf("open home directory: %w", err)
	}
	return &Source{root: root, home: home, limits: limits.withDefaults()}, nil
}

func (source *Source) Close() error { return source.root.Close() }

func (source *Source) Home() string { return source.home }

func (source *Source) Open(name string) (io.ReadCloser, error) {
	relative, err := source.resolve(name, false)
	if err != nil {
		return nil, err
	}
	info, err := source.lstat(relative, name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, name)
	}
	if info.Size() > source.limits.MaxFileBytes {
		return nil, fmt.Errorf("%w: %s", ErrFileLimit, name)
	}
	// O_NOFOLLOW rejects a final component swapped for a symlink after the
	// lstat above; the root handle confines every earlier component.
	file, err := source.root.OpenFile(relative, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		if isSymlinkOpenErr(err) {
			return nil, fmt.Errorf("%w: %s", ErrSymlink, name)
		}
		return nil, err
	}
	return &boundedFile{File: file, path: name, left: source.limits.MaxFileBytes}, nil
}

func (source *Source) ReadDir(name string) ([]fs.DirEntry, error) {
	relative, err := source.resolve(name, true)
	if err != nil {
		return nil, err
	}
	info, err := source.lstat(relative, name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", name)
	}
	directory, err := source.root.OpenFile(relative, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		if isSymlinkOpenErr(err) {
			return nil, fmt.Errorf("%w: %s", ErrSymlink, name)
		}
		return nil, err
	}
	defer directory.Close()
	entries := []fs.DirEntry{}
	for {
		remaining := source.limits.MaxEntries - source.entries.Load()
		if remaining < 0 {
			return nil, ErrEntryLimit
		}
		batchSize := int64(1024)
		if remaining+1 < batchSize {
			batchSize = remaining + 1
		}
		batch, readErr := directory.ReadDir(int(batchSize))
		if source.entries.Add(int64(len(batch))) > source.limits.MaxEntries {
			return nil, ErrEntryLimit
		}
		entries = append(entries, batch...)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	// Symlinked entries are dropped rather than followed, so a link planted in
	// an allowlisted tree is never traversed or parsed.
	result := slices.DeleteFunc(entries, func(entry fs.DirEntry) bool {
		return entry.Type()&fs.ModeSymlink != 0
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Name() < result[j].Name() })
	return result, nil
}

func (source *Source) Stat(name string) (fs.FileInfo, error) {
	relative, err := source.resolve(name, false)
	if err != nil {
		return nil, err
	}
	return source.lstat(relative, name)
}

// lstat never follows a final symlink, so a link is reported as denied instead
// of silently describing its target.
func (source *Source) lstat(relative, name string) (fs.FileInfo, error) {
	info, err := source.root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s", ErrSymlink, name)
	}
	return info, nil
}

// resolve maps an absolute host path to a home-relative name inside the fixed
// allowlist. It is lexical on purpose: containment is enforced by the root
// handle, not by resolving the path a second time.
func (source *Source) resolve(name string, requireTree bool) (string, error) {
	clean := filepath.Clean(name)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w: %s", ErrPathDenied, name)
	}
	relative, err := filepath.Rel(source.home, clean)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrPathDenied, name)
	}
	relative = filepath.ToSlash(relative)
	for _, allowed := range homeAllowlist {
		if requireTree && !allowed.tree {
			continue
		}
		if relative == allowed.relative {
			return relative, nil
		}
		if !allowed.tree || !strings.HasPrefix(relative, allowed.relative+"/") {
			continue
		}
		if depth(relative, allowed.relative) > source.limits.MaxDepth {
			return "", fmt.Errorf("%w: %s", ErrDepthLimit, name)
		}
		return relative, nil
	}
	return "", fmt.Errorf("%w: %s", ErrPathDenied, name)
}

func depth(relative, root string) int {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(relative, root), "/")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "/") + 1
}

// boundedFile stops a transcript read at the helper's per-file limit even when
// the file grows while it is being parsed.
type boundedFile struct {
	*os.File
	path string
	left int64
}

func (file *boundedFile) Read(buffer []byte) (int, error) {
	if file.left <= 0 {
		return 0, fmt.Errorf("%w: %s", ErrFileLimit, file.path)
	}
	if int64(len(buffer)) > file.left {
		buffer = buffer[:file.left]
	}
	read, err := file.File.Read(buffer)
	file.left -= int64(read)
	return read, err
}
