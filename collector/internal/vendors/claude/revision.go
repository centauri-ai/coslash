package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// LocalSourceRevisions uses one directory scan and file metadata to wake the
// queue when raw bytes or parser sidecars change without a parsed-card change.
// The complete producer still hashes and verifies the exact bytes before upload.
func LocalSourceRevisions(ctx context.Context, source vendors.ReadSource, home string) (map[string]string, error) {
	scan, err := ScanSourceContext(ctx, source, ProjectsRoot(home))
	if err != nil || scan.SkippedTotal != 0 {
		return nil, vendors.ErrInvalidData
	}
	groups := map[string][]string{}
	for _, file := range scan.Files {
		id := FamilyIDFromPath(file)
		groups[id] = append(groups[id], file)
		if ParentIDFromPath(file) != "" {
			groups[id] = append(groups[id], strings.TrimSuffix(file, ".jsonl")+".meta.json")
			if strings.Contains(filepath.ToSlash(file), "/subagents/workflows/") {
				runDir := filepath.Dir(strings.Replace(filepath.ToSlash(file), "/subagents/workflows/", "/workflows/", 1))
				groups[id] = append(groups[id], runDir+".json", filepath.Join(filepath.Dir(file), "journal.jsonl"))
			}
		}
	}
	revisions := make(map[string]string, len(groups))
	for id, paths := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sort.Strings(paths)
		hash := sha256.New()
		previous := ""
		for _, file := range paths {
			if file == previous {
				continue
			}
			previous = file
			info, err := source.Stat(file)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			relative, err := vendors.SourcePathRelative(source, home, file)
			if err != nil {
				return nil, err
			}
			hash.Write([]byte(filepath.ToSlash(relative)))
			hash.Write([]byte{0})
			hash.Write([]byte(strconv.FormatInt(info.Size(), 10)))
			hash.Write([]byte{0})
			hash.Write([]byte(strconv.FormatInt(info.ModTime().UnixNano(), 10)))
			hash.Write([]byte{'\n'})
		}
		revisions[id] = hex.EncodeToString(hash.Sum(nil))
	}
	return revisions, nil
}
