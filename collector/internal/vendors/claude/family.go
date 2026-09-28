package claude

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

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
	return ParseFamilyFilesSourceContext(context.Background(), source, files)
}

func ParseFamilyFilesSourceContext(ctx context.Context, source vendors.ReadSource, files []string) ([]*vendors.ParsedSession, error) {
	parsed, _, err := vendors.ParseSourceFilesStrictContext(ctx, source, files, parseSourceContext)
	if err != nil {
		return nil, err
	}
	return finalizeParsedFilesContext(ctx, source, parsed)
}

func CompleteFamilyFilesSourceContext(ctx context.Context, source vendors.ReadSource, home, id string) ([]string, error) {
	scan, err := ScanSourceContext(ctx, source, ProjectsRoot(home))
	if err != nil {
		return nil, err
	}
	if scan.SkippedTotal != 0 {
		return nil, vendors.ErrInvalidData
	}
	return familyFilesContext(ctx, source, scan.Files, id)
}

func ValidateCompleteTranscriptSourceContext(ctx context.Context, source vendors.ReadSource, file string) error {
	rows, err := vendors.ParseJSONLSourceContext[claudeSessionRecord](ctx, source, file)
	if err != nil || len(rows) == 0 {
		return vendors.ErrInvalidData
	}
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rows[i].Message != nil {
			content := bytes.TrimSpace(rows[i].Message.Content)
			if len(content) != 0 && content[0] != '[' && content[0] != '"' && !bytes.Equal(content, []byte("null")) {
				return vendors.ErrInvalidData
			}
			if _, err := rows[i].Message.contentBlocks(); err != nil {
				return vendors.ErrInvalidData
			}
		}
		if _, err := rows[i].toolResult(); err != nil {
			return vendors.ErrInvalidData
		}
		if rows[i].Timestamp != "" {
			if _, err := session.RFC3339ToUnixEpoch(rows[i].Timestamp); err != nil {
				return vendors.ErrInvalidData
			}
		}
	}
	return nil
}

func ValidateCompleteSidecarSourceContext(ctx context.Context, source vendors.ReadSource, file, kind string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch kind {
	case "subagent-meta":
		var meta subagentMeta
		found, err := vendors.ReadJSONSource(source, file, &meta)
		if err != nil || !found {
			return vendors.ErrInvalidData
		}
	case "workflow-state":
		var run workflowRun
		found, err := vendors.ReadJSONSource(source, file, &run)
		if err != nil || !found {
			return vendors.ErrInvalidData
		}
	case "workflow-journal":
		rows, err := vendors.ParseJSONLSourceContext[workflowJournalEntry](ctx, source, file)
		if err != nil || len(rows) == 0 {
			return vendors.ErrInvalidData
		}
	}
	return nil
}
