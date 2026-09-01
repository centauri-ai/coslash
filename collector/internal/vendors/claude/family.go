package claude

import (
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// FamilyIDFromPath names the card a transcript belongs to: a subagent joins its
// parent root, a root names itself. Helper collection groups files this way
// before it decides which families changed.
func FamilyIDFromPath(file string) string {
	return familyIDFromPath(file)
}

// SessionIDFromPath returns the session a transcript file carries. Subagent
// files are named agent-<id>.jsonl, so the file basename is the identity for
// both roots and children.
func SessionIDFromPath(file string) string {
	return strings.TrimSuffix(filepath.Base(file), ".jsonl")
}

// ParseFamilyFilesSource parses one selected file set through the same pipeline
// remote collection uses, returning every main-file failure to the caller so
// one bad transcript can be isolated to its own family.
func ParseFamilyFilesSource(
	source vendors.ReadSource,
	files []string,
) ([]*vendors.ParsedSession, error) {
	return parseFilesSourceStrict(source, files)
}
