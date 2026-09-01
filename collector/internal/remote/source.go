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
	"sync"
	"sync/atomic"
	"unicode/utf8"
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

// dirCacheEntry records a child discovered by an already-validated ReadDir.
// Its non-symlink mode bit came from that directory listing (SFTP reports
// per-entry attributes without following, the same proof a separate lstat
// would give), and its canonical path is the parent's already-proven
// canonical prefix plus a single traversal-free name. Reusing it lets
// validate skip a redundant lstat+realpath round trip for the same session
// without weakening the symlink or allowlist check.
type dirCacheEntry struct {
	allowedIndex int
	canonical    string
	info         fs.FileInfo
}

// Source exposes bounded read-only access to approved agent data.
type Source struct {
	ops     sftpOperations
	home    string
	allowed []allowedPath
	limits  Limits

	entries atomic.Int64
	bytes   atomic.Int64

	dirCache sync.Map // cleaned lexical path -> dirCacheEntry
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

// Limits returns the effective (default-filled) limits this Source enforces.
func (source *Source) Limits() Limits {
	return source.limits
}

// vendorBudget is an independent byte allowance so one vendor's large files
// cannot starve another vendor collecting concurrently through the same
// Source.
type vendorBudget struct {
	used  *atomic.Int64
	limit int64
}

// ForVendor returns a view of Source with its own byte budget. Path
// validation, the directory cache, and the entry-count limit remain shared.
func (source *Source) ForVendor(maxBytes int64) *VendorSource {
	if maxBytes <= 0 {
		maxBytes = source.limits.MaxTotalBytes
	}
	return &VendorSource{source: source, budget: &vendorBudget{used: &atomic.Int64{}, limit: maxBytes}}
}

// VendorSource implements vendors.ReadSource with a private byte budget.
type VendorSource struct {
	source *Source
	budget *vendorBudget
}

func (v *VendorSource) Open(name string) (io.ReadCloser, error)    { return v.source.open(name, v.budget) }
func (v *VendorSource) ReadDir(name string) ([]fs.DirEntry, error) { return v.source.ReadDir(name) }
func (v *VendorSource) Stat(name string) (fs.FileInfo, error)      { return v.source.Stat(name) }
func (v *VendorSource) FreshStat(name string) (fs.FileInfo, error) { return v.source.freshStat(name) }

func (source *Source) Open(name string) (io.ReadCloser, error) {
	return source.open(name, &vendorBudget{used: &source.bytes, limit: source.limits.MaxTotalBytes})
}

func (source *Source) open(name string, budget *vendorBudget) (io.ReadCloser, error) {
	// Opening follows the live remote path, so manifest metadata alone is not
	// sufficient security proof: re-run lstat+realpath immediately before the
	// SFTP open to reject a child replaced by a symlink after ReadDir.
	_, allowed, info, err := source.validateIndexed(name, false, false)
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
		total:       budget.used,
		totalLimit:  budget.limit,
	}, nil
}

// StatSize reports a candidate file's size without opening it, so an
// oversized file can be skipped before any header or body read is attempted.
func (source *Source) StatSize(name string) (int64, error) {
	_, info, err := source.validate(name, false)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (source *Source) ReadDir(name string) ([]fs.DirEntry, error) {
	// readDir follows the live path too. A cached parent entry may have been
	// replaced since it was listed, so directories require fresh validation.
	allowedIndex, allowed, info, err := source.validateIndexed(name, true, false)
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
	clean := path.Clean(name)
	result := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !validRemoteDirEntryName(entry.Name()) {
			return nil, ErrPathDenied
		}
		result = append(result, fs.FileInfoToDirEntry(entry))
		if allowedIndex >= 0 && entry.Mode()&fs.ModeSymlink == 0 {
			childLexical := path.Join(clean, entry.Name())
			childCanonical := path.Join(allowed, entry.Name())
			source.dirCache.Store(childLexical, dirCacheEntry{
				allowedIndex: allowedIndex, canonical: childCanonical, info: entry,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name() < result[j].Name() })
	return result, nil
}

// validRemoteDirEntryName rejects malformed names from an untrusted SFTP
// server before they are used to build a cached child path. POSIX servers do
// not normally return separators here, but this boundary must not rely on a
// compromised server obeying that convention.
func validRemoteDirEntryName(name string) bool {
	return utf8.ValidString(name) && name != "" && name != "." && name != ".." && path.Base(name) == name &&
		!strings.ContainsAny(name, "/\\\x00")
}

func (source *Source) Stat(name string) (fs.FileInfo, error) {
	_, info, err := source.validate(name, false)
	return info, err
}

func (source *Source) freshStat(name string) (fs.FileInfo, error) {
	_, _, info, err := source.validateIndexed(name, false, false)
	return info, err
}

func (source *Source) validate(name string, requireTree bool) (string, fs.FileInfo, error) {
	_, canonical, info, err := source.validateIndexed(name, requireTree, true)
	return canonical, info, err
}

func (source *Source) validateIndexed(name string, requireTree, allowCached bool) (int, string, fs.FileInfo, error) {
	clean := path.Clean(name)
	if procPID(clean) {
		info, err := source.ops.lstat(clean)
		if err != nil {
			return -1, "", nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return -1, "", nil, fmt.Errorf("%w: %s", ErrSymlink, name)
		}
		return -1, clean, info, nil
	}
	// Every cache entry comes from a ReadDir, which only succeeds against a
	// tree-type allowedPath, so a cache hit always satisfies requireTree.
	if cached, ok := source.dirCache.Load(clean); allowCached && ok {
		entry := cached.(dirCacheEntry)
		allowed := &source.allowed[entry.allowedIndex]
		if pathDepth(allowed.lexical, clean) > source.limits.MaxDepth {
			return -1, "", nil, fmt.Errorf("%w: %s", ErrDepthLimit, name)
		}
		if allowed.canonical == "" || !within(allowed.canonical, entry.canonical, allowed.tree) {
			return -1, "", nil, fmt.Errorf("%w: %s", ErrPathDenied, name)
		}
		return entry.allowedIndex, entry.canonical, entry.info, nil
	}
	allowedIndex, allowed := source.match(clean, requireTree)
	if allowed == nil {
		return -1, "", nil, fmt.Errorf("%w: %s", ErrPathDenied, name)
	}
	if allowed.tree && pathDepth(allowed.lexical, clean) > source.limits.MaxDepth {
		return -1, "", nil, fmt.Errorf("%w: %s", ErrDepthLimit, name)
	}
	info, err := source.ops.lstat(clean)
	if err != nil {
		return -1, "", nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return -1, "", nil, fmt.Errorf("%w: %s", ErrSymlink, name)
	}
	canonical, err := source.ops.realPath(clean)
	if err != nil {
		return -1, "", nil, err
	}
	canonical = path.Clean(canonical)
	if allowed.canonical == "" || !within(allowed.canonical, canonical, allowed.tree) {
		return -1, "", nil, fmt.Errorf("%w: %s", ErrPathDenied, name)
	}
	return allowedIndex, canonical, info, nil
}

func (source *Source) match(name string, requireTree bool) (int, *allowedPath) {
	for index := range source.allowed {
		allowed := &source.allowed[index]
		if requireTree && !allowed.tree {
			continue
		}
		if within(allowed.lexical, name, allowed.tree) {
			return index, allowed
		}
	}
	return -1, nil
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
