package vendors

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const MaxCandidateFilesPerAgent = 2_000

// ReadSource is the file access needed by transcript discovery and parsing.
type ReadSource interface {
	Open(string) (io.ReadCloser, error)
	ReadDir(string) ([]fs.DirEntry, error)
	Stat(string) (fs.FileInfo, error)
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

func LimitNewestSourceFiles(
	source ReadSource,
	files []string,
	limit int,
) ([]string, bool) {
	if limit <= 0 || len(files) <= limit {
		return files, false
	}
	type candidate struct {
		path     string
		modified int64
	}
	candidates := make([]candidate, 0, len(files))
	for _, file := range files {
		candidates = append(candidates, candidate{
			path: file, modified: SourceModificationTime(source, file),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modified == candidates[j].modified {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].modified > candidates[j].modified
	})
	selected := make(map[string]struct{}, limit)
	for _, candidate := range candidates[:limit] {
		selected[candidate.path] = struct{}{}
	}
	result := make([]string, 0, limit)
	for _, file := range files {
		if _, ok := selected[file]; ok {
			result = append(result, file)
		}
	}
	return result, true
}

func walkReadSource(source ReadSource, root string, visit fs.WalkDirFunc) error {
	info, err := source.Stat(root)
	if err != nil {
		return visit(root, nil, err)
	}
	return walkReadSourceEntry(source, root, fs.FileInfoToDirEntry(info), visit)
}

func walkReadSourceEntry(
	source ReadSource,
	path string,
	entry fs.DirEntry,
	visit fs.WalkDirFunc,
) error {
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
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, child := range entries {
		childPath := filepath.Join(path, child.Name())
		if err := walkReadSourceEntry(source, childPath, child, visit); err != nil {
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
	file, err := source.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var records []T
	for {
		var record T
		err := decoder.Decode(&record)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
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
