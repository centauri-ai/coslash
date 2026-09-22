package vendors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// ErrInvalidData marks transcript content that cannot be parsed or validated.
var ErrInvalidData = errors.New("invalid transcript data")

// AggregateFingerprint identifies all inputs that can change a collected
// family, including metadata that is stored outside transcript files.
func AggregateFingerprint(familyID string, fingerprints []FileFingerprint, sessionIDs []string, metadata *SessionMetadata) string {
	fingerprints = append([]FileFingerprint(nil), fingerprints...)
	sort.Slice(fingerprints, func(i, j int) bool { return fingerprints[i].Key < fingerprints[j].Key })
	sessionIDs = append([]string(nil), sessionIDs...)
	sort.Strings(sessionIDs)

	digest := sha256.New()
	fmt.Fprintf(digest, "v1\n%s\n%s\n", ParserVersion, familyID)
	previousKey := ""
	for _, fingerprint := range fingerprints {
		if fingerprint.Key == previousKey {
			continue
		}
		previousKey = fingerprint.Key
		fmt.Fprintf(
			digest, "f\t%s\t%s\t%s\n", fingerprint.Key,
			strconv.FormatInt(fingerprint.Size, 10),
			strconv.FormatInt(fingerprint.ModifiedAtMs, 10),
		)
	}
	previousID := ""
	if metadata != nil {
		for _, id := range sessionIDs {
			if id == previousID {
				continue
			}
			previousID = id
			if enrichment := metadata.Lookup(id); enrichment != nil {
				encoded, _ := json.Marshal(enrichment)
				fmt.Fprintf(digest, "m\t%s\t%s\n", id, encoded)
			}
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func FingerprintSourceFiles(
	source ReadSource,
	root string,
	files []string,
) ([]FileFingerprint, error) {
	return FingerprintSourceFilesContext(context.Background(), source, root, files)
}

func FingerprintSourceFilesContext(
	ctx context.Context,
	source ReadSource,
	root string,
	files []string,
) ([]FileFingerprint, error) {
	fingerprints := make([]FileFingerprint, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := source.Stat(file)
		if err != nil {
			return nil, err
		}
		relative, err := SourcePathRelative(source, root, file)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(filepath.ToSlash(relative)))
		fingerprints = append(fingerprints, FileFingerprint{
			Key: hex.EncodeToString(digest[:]), Size: info.Size(),
			ModifiedAtMs: info.ModTime().UnixMilli(),
		})
	}
	return fingerprints, nil
}

const MaxCandidateFilesPerAgent = 2_000

// ReadSource is the file access needed by transcript discovery and parsing.
type ReadSource interface {
	Open(string) (io.ReadCloser, error)
	ReadDir(string) ([]fs.DirEntry, error)
	Stat(string) (fs.FileInfo, error)
}

type sourcePathOperations interface {
	JoinPath(...string) string
	RelativePath(string, string) (string, error)
	DirPath(string) string
	BasePath(string) string
}

// SourcePathJoin and SourcePathRelative use the path semantics of the source.
// Local sources follow the host OS; SSH/SFTP sources use POSIX paths even when
// the collector itself runs on Windows.
func SourcePathJoin(source ReadSource, elements ...string) string {
	if operations, ok := source.(sourcePathOperations); ok {
		return operations.JoinPath(elements...)
	}
	return filepath.Join(elements...)
}

func SourcePathRelative(source ReadSource, root, name string) (string, error) {
	if operations, ok := source.(sourcePathOperations); ok {
		return operations.RelativePath(root, name)
	}
	return filepath.Rel(root, name)
}

func SourcePathDir(source ReadSource, name string) string {
	if operations, ok := source.(sourcePathOperations); ok {
		return operations.DirPath(name)
	}
	return filepath.Dir(name)
}

func SourcePathBase(source ReadSource, name string) string {
	if operations, ok := source.(sourcePathOperations); ok {
		return operations.BasePath(name)
	}
	return filepath.Base(name)
}

// freshStatSource is implemented by remote sources that cache directory
// metadata. FreshStat bypasses that manifest cache for post-read stability and
// path-security checks. Local sources can simply use Stat.
type freshStatSource interface {
	FreshStat(string) (fs.FileInfo, error)
}

func FingerprintSourceFilesFresh(source ReadSource, root string, files []string) ([]FileFingerprint, error) {
	return FingerprintSourceFilesFreshContext(context.Background(), source, root, files)
}

func FingerprintSourceFilesFreshContext(ctx context.Context, source ReadSource, root string, files []string) ([]FileFingerprint, error) {
	fresh, ok := source.(freshStatSource)
	if !ok {
		return FingerprintSourceFilesContext(ctx, source, root, files)
	}
	fingerprints := make([]FileFingerprint, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := fresh.FreshStat(file)
		if err != nil {
			return nil, err
		}
		relative, err := SourcePathRelative(source, root, file)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(filepath.ToSlash(relative)))
		fingerprints = append(fingerprints, FileFingerprint{
			Key: hex.EncodeToString(digest[:]), Size: info.Size(), ModifiedAtMs: info.ModTime().UnixMilli(),
		})
	}
	return fingerprints, nil
}

type osReadSource struct{}

func (osReadSource) Open(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (osReadSource) ReadDir(path string) ([]fs.DirEntry, error) {
	return os.ReadDir(path)
}

func (osReadSource) Stat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}

// LocalReadSource uses the host filesystem.
var LocalReadSource ReadSource = osReadSource{}

func SourceModificationTime(source ReadSource, path string) int64 {
	info, err := source.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixMilli()
}

// LimitNewestSourceFileFamilies keeps whole file families, ordered by their newest member.
func LimitNewestSourceFileFamilies(
	source ReadSource,
	files []string,
	limit int,
	familyID func(string) string,
) ([]string, bool) {
	return LimitNewestFileFamilies(files, limit, familyID, func(path string) int64 {
		return SourceModificationTime(source, path)
	})
}

func LimitNewestFileFamilies(
	files []string,
	limit int,
	familyID func(string) string,
	modified func(string) int64,
) ([]string, bool) {
	result, truncated, _ := LimitNewestFileFamiliesContext(context.Background(), files, limit, familyID, modified)
	return result, truncated
}

func LimitNewestFileFamiliesContext(
	ctx context.Context,
	files []string,
	limit int,
	familyID func(string) string,
	modified func(string) int64,
) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if limit <= 0 || len(files) <= limit {
		return files, false, nil
	}
	type family struct {
		id       string
		files    map[string]struct{}
		modified int64
	}
	families := map[string]*family{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		id := familyID(file)
		item := families[id]
		if item == nil {
			item = &family{id: id, files: map[string]struct{}{}}
			families[id] = item
		}
		item.files[file] = struct{}{}
		item.modified = max(item.modified, modified(file))
	}
	ordered := make([]*family, 0, len(families))
	for _, item := range families {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].modified == ordered[j].modified {
			return ordered[i].id < ordered[j].id
		}
		return ordered[i].modified > ordered[j].modified
	})
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	selected := map[string]struct{}{}
	for _, item := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if len(selected) > 0 && len(selected)+len(item.files) > limit {
			continue
		}
		for file := range item.files {
			selected[file] = struct{}{}
		}
	}
	result := make([]string, 0, len(selected))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if _, ok := selected[file]; ok {
			result = append(result, file)
		}
	}
	return result, len(result) < len(files), nil
}

func walkReadSource(source ReadSource, root string, visit fs.WalkDirFunc) error {
	return walkReadSourceContext(context.Background(), source, root, visit)
}

func walkReadSourceContext(ctx context.Context, source ReadSource, root string, visit fs.WalkDirFunc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := source.Stat(root)
	if err != nil {
		return visit(root, nil, err)
	}
	return walkReadSourceEntryContext(ctx, source, root, fs.FileInfoToDirEntry(info), visit)
}

func walkReadSourceEntry(
	source ReadSource,
	path string,
	entry fs.DirEntry,
	visit fs.WalkDirFunc,
) error {
	return walkReadSourceEntryContext(context.Background(), source, path, entry, visit)
}

func walkReadSourceEntryContext(
	ctx context.Context,
	source ReadSource,
	path string,
	entry fs.DirEntry,
	visit fs.WalkDirFunc,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := visit(path, entry, nil)
	if err != nil || !entry.IsDir() {
		if errors.Is(err, fs.SkipDir) && !entry.IsDir() {
			return nil
		}
		return err
	}
	entries, readErr := source.ReadDir(path)
	if readErr != nil {
		if err := visit(path, entry, readErr); errors.Is(err, fs.SkipDir) {
			return nil
		} else if err != nil {
			return err
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, child := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		childPath := SourcePathJoin(source, path, child.Name())
		if err := walkReadSourceEntryContext(ctx, source, childPath, child, visit); err != nil {
			if errors.Is(err, fs.SkipDir) {
				continue
			}
			return err
		}
	}
	return nil
}

// ParseJSONLSource decodes JSON values from a source file until clean or torn EOF.
func ParseJSONLSource[T any](source ReadSource, path string) ([]T, error) {
	return ParseJSONLSourceContext[T](context.Background(), source, path)
}

func ParseJSONLSourceContext[T any](ctx context.Context, source ReadSource, path string) ([]T, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := source.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(contextReader{ctx: ctx, reader: file})
	var records []T
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var record T
		err := decoder.Decode(&record)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, fmt.Errorf("%w: %w", ErrInvalidData, err)
		}
		records = append(records, record)
	}
	return records, nil
}

// ReadJSONSource decodes one JSON object and treats malformed content as absent.
func ReadJSONSource(source ReadSource, path string, value any) (bool, error) {
	file, err := source.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(value); err != nil {
		return false, nil
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return false, nil
	}
	return true, nil
}
