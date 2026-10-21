package grok

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// NonChildSummaryPaths names the exact external summaries that verified exclusions.
func (family FamilyRef) NonChildSummaryPaths() []string {
	paths := make([]string, 0, len(family.nonChildDirs))
	for _, dir := range family.nonChildDirs {
		paths = append(paths, filepath.Join(dir, "summary.json"))
	}
	sort.Strings(paths)
	return paths
}

// ParseFrozenFamilyContext reads only frozen directories and summary proofs.
// family must be the intact planner result; its original paths are never opened.
func ParseFrozenFamilyContext(ctx context.Context, family FamilyRef, dirs, proofs []string) ([]*vendors.ParsedSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	invalid := errors.New("grok: invalid frozen family")
	if len(family.memberIDs) == 0 || len(family.memberIDs) != len(dirs) || len(proofs) != len(family.nonChildren) {
		return nil, invalid
	}
	exclusions := map[string]bool{}
	for _, path := range proofs {
		summary, err := readSummary(filepath.Dir(path))
		if err != nil || filepath.Base(path) != "summary.json" || summary.ChatFormatVersion != 1 || isSubagentKind(summary.SessionKind) || !family.nonChildren[summary.Info.ID] || exclusions[summary.Info.ID] {
			return nil, invalid
		}
		exclusions[summary.Info.ID] = true
	}
	for _, dir := range dirs {
		if err := validateFrozenInputs(ctx, dir); err != nil {
			return nil, err
		}
	}
	parsed, err := parseFamilyDirsContext(ctx, dirs, exclusions)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(parsed))
	for _, item := range parsed {
		ids = append(ids, item.Session.ID)
	}
	if !slices.Equal(ids, family.memberIDs) || ids[0] != family.ID {
		return nil, invalid
	}
	return parsed, nil
}

func validateFrozenInputs(ctx context.Context, dir string) error {
	if dir == "" {
		return errors.New("grok: invalid frozen input")
	}
	root := filepath.Clean(dir)
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("grok: invalid frozen input")
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("grok: invalid frozen input")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !IsSourceFile(relative) {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("grok: invalid frozen input")
		}
		var target any
		switch filepath.ToSlash(relative) {
		case "summary.json":
			target = &summaryFile{}
		case "usage.json":
			target = &usageFile{}
		case "signals.json":
			target = &signalsFile{}
		case "plan.json":
			target = &struct {
				Todos map[string]struct {
					Content string `json:"content"`
					Status  string `json:"status"`
				} `json:"todos"`
			}{}
		case "plan_mode.json":
			target = &struct {
				Awaiting bool `json:"awaiting_plan_approval"`
			}{}
		case "prompt_context.json":
			target = &struct {
				NonInteractive bool `json:"is_non_interactive"`
			}{}
		case "goal/state.json":
			target = &struct {
				Objective string `json:"objective"`
			}{}
		case "plan.md":
			return nil
		case "updates.jsonl":
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 64*1024), int(maxGrokJSONBytes))
			for scanner.Scan() {
				if err := ctx.Err(); err != nil {
					return err
				}
				var line updateLine
				if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
					return err
				}
			}
			return scanner.Err()
		default:
			switch {
			case strings.HasPrefix(filepath.ToSlash(relative), "compaction_checkpoints/"):
				target = &struct {
					CreatedAt string `json:"created_at"`
					History   []struct {
						Type    string          `json:"type"`
						Content json.RawMessage `json:"content"`
					} `json:"compacted_history"`
				}{}
			case filepath.Base(path) == "meta.json":
				target = &subagentMeta{}
			case filepath.Base(path) == "output.json":
				target = &struct {
					Output string `json:"output"`
				}{}
			}
		}
		if target != nil {
			return readJSON(path, target)
		}
		return nil
	})
}
