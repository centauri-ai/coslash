package remote

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

type sftpOperations struct {
	realPath func(string) (string, error)
	lstat    func(string) (os.FileInfo, error)
	readDir  func(string) ([]os.FileInfo, error)
	open     func(string) (io.ReadCloser, error)
}

type allowedPath struct {
	lexical   string
	canonical string
	tree      bool
}

// Source exposes bounded read-only access to approved agent data.
type Source struct {
	ops     sftpOperations
	home    string
	allowed []allowedPath
	limits  Limits

	entries atomic.Int64
	bytes   atomic.Int64
}

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

func newSource(ops sftpOperations, limits Limits) (*Source, error) {
	home, err := ops.realPath(".")
	if err != nil {
		return nil, fmt.Errorf("resolve SFTP home: %w", err)
	}
	home = path.Clean(home)
	source := &Source{ops: ops, home: home, limits: limits.withDefaults()}
	for _, item := range homeAllowlist {
		lexical := path.Join(home, item.relative)
		canonical := ""
		if info, err := ops.lstat(lexical); err == nil {
			if info.Mode()&fs.ModeSymlink != 0 {
				return nil, fmt.Errorf("%w: %s", ErrSymlink, item.relative)
			}
			canonical, err = ops.realPath(lexical)
			if err != nil {
				return nil, fmt.Errorf("resolve allowed path %s: %w", item.relative, err)
			}
			canonical = path.Clean(canonical)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("inspect allowed path %s: %w", item.relative, err)
		}
		source.allowed = append(source.allowed, allowedPath{
			lexical: lexical, canonical: canonical, tree: item.tree,
		})
	}
	return source, nil
}

func (source *Source) Home() string {
	return source.home
}

func (source *Source) Open(name string) (io.ReadCloser, error) {
	allowed, info, err := source.validate(name, false)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("remote file is not regular: %s", name)
	}
	if info.Size() > source.limits.MaxFileBytes {
		return nil, fmt.Errorf("%w: %s", ErrFileLimit, name)
	}
	file, err := source.ops.open(allowed)
	if err != nil {
		return nil, err
	}
	return &boundedRemoteFile{
		ReadCloser:  file,
		path:        name,
		fileLeft:    source.limits.MaxFileBytes,
		contentLeft: info.Size(),
		total:       &source.bytes,
		totalLimit:  source.limits.MaxTotalBytes,
	}, nil
}

func (source *Source) ReadDir(name string) ([]fs.DirEntry, error) {
	allowed, info, err := source.validate(name, true)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("remote path is not a directory: %s", name)
	}
	entries, err := source.ops.readDir(allowed)
	if err != nil {
		return nil, err
	}
	if source.entries.Add(int64(len(entries))) > source.limits.MaxEntries {
		return nil, ErrEntryLimit
	}
	result := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, fs.FileInfoToDirEntry(entry))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name() < result[j].Name() })
	return result, nil
}

func (source *Source) Stat(name string) (fs.FileInfo, error) {
	_, info, err := source.validate(name, false)
	return info, err
}

func (source *Source) validate(name string, requireTree bool) (string, fs.FileInfo, error) {
	clean := path.Clean(name)
	if procPID(clean) {
		info, err := source.ops.lstat(clean)
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("%w: %s", ErrSymlink, name)
		}
		return clean, info, nil
	}
	allowed := source.match(clean, requireTree)
	if allowed == nil {
		return "", nil, fmt.Errorf("%w: %s", ErrPathDenied, name)
	}
	if allowed.tree && pathDepth(allowed.lexical, clean) > source.limits.MaxDepth {
		return "", nil, fmt.Errorf("%w: %s", ErrDepthLimit, name)
	}
	info, err := source.ops.lstat(clean)
	if err != nil {
		return "", nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("%w: %s", ErrSymlink, name)
	}
	canonical, err := source.ops.realPath(clean)
	if err != nil {
		return "", nil, err
	}
	canonical = path.Clean(canonical)
	if allowed.canonical == "" || !within(allowed.canonical, canonical, allowed.tree) {
		return "", nil, fmt.Errorf("%w: %s", ErrPathDenied, name)
	}
	return canonical, info, nil
}

func (source *Source) match(name string, requireTree bool) *allowedPath {
	for index := range source.allowed {
		allowed := &source.allowed[index]
		if requireTree && !allowed.tree {
			continue
		}
		if within(allowed.lexical, name, allowed.tree) {
			return allowed
		}
	}
	return nil
}

func within(root, candidate string, tree bool) bool {
	if candidate == root {
		return true
	}
	return tree && strings.HasPrefix(candidate, root+"/")
}

func pathDepth(root, candidate string) int {
	relative := strings.TrimPrefix(strings.TrimPrefix(candidate, root), "/")
	if relative == "" {
		return 0
	}
	return strings.Count(relative, "/") + 1
}

func procPID(name string) bool {
	if !strings.HasPrefix(name, "/proc/") {
		return false
	}
	pid := strings.TrimPrefix(name, "/proc/")
	if pid == "" || strings.Contains(pid, "/") {
		return false
	}
	value, err := strconv.Atoi(pid)
	return err == nil && value > 0
}

type boundedRemoteFile struct {
	io.ReadCloser
	path        string
	fileLeft    int64
	contentLeft int64
	total       *atomic.Int64
	totalLimit  int64
	exhausted   bool
}

func (file *boundedRemoteFile) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if file.exhausted {
		return 0, fmt.Errorf("%w: %s", ErrFileLimit, file.path)
	}
	if file.contentLeft == 0 {
		return 0, io.EOF
	}
	requested := min(int64(len(buffer)), file.fileLeft+1)
	reserved := file.reserve(requested)
	if reserved == 0 {
		return 0, fmt.Errorf("%w: %s", ErrTotalLimit, file.path)
	}
	buffer = buffer[:reserved]
	n, err := file.ReadCloser.Read(buffer)
	if int64(n) < reserved {
		file.total.Add(int64(n) - reserved)
	}
	file.fileLeft -= int64(n)
	file.contentLeft -= int64(n)
	if file.fileLeft < 0 {
		file.exhausted = true
		return n, fmt.Errorf("%w: %s", ErrFileLimit, file.path)
	}
	if reserved < requested && file.contentLeft > 0 {
		return n, fmt.Errorf("%w: %s", ErrTotalLimit, file.path)
	}
	return n, err
}

func (file *boundedRemoteFile) reserve(requested int64) int64 {
	for {
		used := file.total.Load()
		if used >= file.totalLimit {
			return 0
		}
		reserved := min(requested, file.totalLimit-used)
		if file.total.CompareAndSwap(used, used+reserved) {
			return reserved
		}
	}
}
