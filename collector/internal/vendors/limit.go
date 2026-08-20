package vendors

import (
	"os"
	"slices"
)

type rootCandidate struct {
	path    string
	mtimeMs int64
}

// LimitRootTranscripts keeps the newest maxRoots root transcripts and every
// non-root file whose parent root remains selected. Ordering is newest-first by
// file modification time before any parsing.
func LimitRootTranscripts(
	files []string,
	maxRoots int,
	parentID func(string) string,
	rootID func(string) string,
) (limited []string, truncated bool) {
	if maxRoots < 0 {
		maxRoots = 0
	}
	roots := make([]rootCandidate, 0, len(files))
	children := make([]string, 0, len(files))
	for _, file := range files {
		if parentID(file) != "" {
			children = append(children, file)
			continue
		}
		mtimeMs := int64(0)
		if info, err := os.Stat(file); err == nil {
			mtimeMs = info.ModTime().UnixMilli()
		}
		roots = append(roots, rootCandidate{path: file, mtimeMs: mtimeMs})
	}
	slices.SortFunc(roots, func(a, b rootCandidate) int {
		switch {
		case a.mtimeMs > b.mtimeMs:
			return -1
		case a.mtimeMs < b.mtimeMs:
			return 1
		case a.path < b.path:
			return -1
		case a.path > b.path:
			return 1
		default:
			return 0
		}
	})
	if len(roots) > maxRoots {
		roots = roots[:maxRoots]
		truncated = true
	}
	keptRoots := make(map[string]struct{}, len(roots))
	limited = make([]string, 0, len(roots)+len(children))
	for _, root := range roots {
		keptRoots[rootID(root.path)] = struct{}{}
		limited = append(limited, root.path)
	}
	for _, file := range children {
		if _, ok := keptRoots[parentID(file)]; ok {
			limited = append(limited, file)
		}
	}
	return limited, truncated
}
