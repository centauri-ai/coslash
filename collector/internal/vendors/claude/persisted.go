package claude

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Claude Code replaces a tool result that is too large with a wrapper and saves
// the full text in tool-results/ beside the session. See docs/vendors/claude-code.md.
const (
	persistedOutputOpen  = "<persisted-output>\n"
	persistedOutputClose = "\n</persisted-output>"
	persistedOutputSaved = "Full output saved to: "
)

// persistedOutput splits a wrapper into the path it names and its preview text.
// The header line and the preview title are dropped, so the preview carries no path.
func persistedOutput(content string) (path, preview string, ok bool) {
	body, found := strings.CutPrefix(content, persistedOutputOpen)
	if !found {
		return "", "", false
	}
	body = strings.TrimSuffix(strings.TrimRight(body, "\r\n\t "), persistedOutputClose)
	header, rest, _ := strings.Cut(body, "\n")
	_, path, found = strings.Cut(header, persistedOutputSaved)
	if !found {
		return "", "", false
	}
	preview = strings.TrimPrefix(rest, "\n")
	if title, after, found := strings.Cut(preview, "\n"); found && strings.HasPrefix(title, "Preview") {
		preview = after
	}
	return path, preview, true
}

// toolResultSessionDir is the directory that holds the tool-results directory
// of the session that owns the transcript: the transcript's own directory for
// a root session, and the parent session's for a subagent or workflow agent.
func toolResultSessionDir(transcript string) string {
	parts := strings.Split(filepath.ToSlash(transcript), "/")
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i] == "subagents" {
			return strings.Join(parts[:i], "/")
		}
	}
	return strings.TrimSuffix(filepath.ToSlash(transcript), ".jsonl")
}

// persistedOutputFile maps the path in a wrapper to the file in the owning
// session's own tool-results directory. The directory prefix of the wrapper is
// ignored, because the home directory can differ between the machine that wrote
// the transcript and the one that reads it. A path that names another session,
// another directory, or a name with separators is refused.
func persistedOutputFile(source vendors.ReadSource, transcript, wrapperPath string) (string, bool) {
	parts := strings.FieldsFunc(wrapperPath, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) < 3 {
		return "", false
	}
	name, dir, owner := parts[len(parts)-1], parts[len(parts)-2], parts[len(parts)-3]
	sessionDir := toolResultSessionDir(transcript)
	if dir != "tool-results" || owner != filepath.Base(sessionDir) || name == "." || name == ".." || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", false
	}
	return vendors.SourcePathJoin(source, filepath.FromSlash(sessionDir), "tool-results", name), true
}

// commandOutput is the truncated output to record for a command's tool result.
// A wrapper whose file is readable gives the file's text; any other wrapper
// gives its preview. The second result is false when a wrapper fell back to its
// preview, which a later parse can improve once the file exists.
// The third result is true when the wrapper names a tool-results file.
func commandOutput(source vendors.ReadSource, transcript, content string) (string, bool, bool) {
	path, preview, ok := persistedOutput(content)
	if !ok {
		if strings.HasPrefix(content, "<persisted-output>") && strings.Contains(content, persistedOutputSaved) {
			// A wrapper in a shape that is not known still must not carry its path.
			return session.TruncateOutput(withoutSavedPath(content)), false, false
		}
		return session.TruncateOutput(content), true, false
	}
	if file, ok := persistedOutputFile(source, transcript, path); ok {
		if text, err := readPersistedOutput(source, file); err == nil {
			return text, true, true
		}
		return session.TruncateOutput(preview), false, true
	}
	return session.TruncateOutput(preview), false, false
}

func withoutSavedPath(content string) string {
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.Contains(line, persistedOutputSaved) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func readPersistedOutput(source vendors.ReadSource, file string) (string, error) {
	// A link could point out of the session's directory, so neither the file
	// nor its tool-results directory may be one.
	stat := source.Stat
	dir := vendors.SourcePathDir(source, file)
	if local, ok := source.(interface {
		Lstat(string) (fs.FileInfo, error)
	}); ok {
		stat = local.Lstat
		if info, err := stat(dir); err != nil || !info.IsDir() {
			return "", vendors.ErrInvalidData
		}
	}
	info, err := stat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", vendors.ErrInvalidData
	}
	input, err := source.Open(file)
	if err != nil {
		return "", err
	}
	defer input.Close()
	return session.TruncateOutputReader(input)
}

// ToolResultFilesSourceContext lists, sorted and without duplicates, the files
// that the command outputs of a transcript read. The parser and the complete
// capture both call it, so they agree on which files belong to a session.
func ToolResultFilesSourceContext(ctx context.Context, source vendors.ReadSource, transcript string) ([]string, error) {
	rows, err := vendors.ParseJSONLSourceContext[claudeSessionRecord](ctx, source, transcript)
	if err != nil {
		return nil, err
	}
	commands := map[string]bool{}
	files := map[string]bool{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.Message == nil {
			continue
		}
		blocks, err := row.Message.contentBlocks()
		if err != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_use" && block.ID != "" && block.Input.Command != "" {
				commands[block.ID] = true
			}
			if block.Type != "tool_result" || !commands[block.ToolUseID] {
				continue
			}
			path, _, ok := persistedOutput(claudeToolOutput(block.Content))
			if !ok {
				continue
			}
			if file, ok := persistedOutputFile(source, transcript, path); ok {
				files[file] = true
			}
		}
	}
	sorted := make([]string, 0, len(files))
	for file := range files {
		sorted = append(sorted, file)
	}
	sort.Strings(sorted)
	return sorted, nil
}
