package sessiondetail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func (p *Projector) readRows(ctx context.Context, path string) ([]json.RawMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := io.LimitReader(file, p.maxTranscriptBytes+1)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), int(min(p.maxTranscriptBytes+1, 4<<20)))
	rows := make([]json.RawMessage, 0, 128)
	var consumed int64
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		consumed += int64(len(scanner.Bytes()) + 1)
		if consumed > p.maxTranscriptBytes {
			return nil, ErrTranscriptTooLarge
		}
		if len(line) == 0 {
			continue
		}
		if len(rows) >= p.maxRows {
			return nil, ErrTranscriptTooLarge
		}
		if !json.Valid(line) {
			return nil, ErrMalformedTranscript
		}
		rows = append(rows, bytes.Clone(line))
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, ErrTranscriptTooLarge
		}
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	return rows, nil
}

func (h *heavyDetail) turn(index int) *Turn {
	if index < 1 {
		index = 1
	}
	if turn, ok := h.turns[index]; ok {
		return turn
	}
	turn := &Turn{Index: index, Todos: []Todo{}, Decisions: []Decision{}, FileEdits: []string{}}
	h.turns[index] = turn
	h.turnOrder = append(h.turnOrder, index)
	return turn
}

func (h *heavyDetail) addTriggered(kind, name string) {
	if name == "" {
		return
	}
	key := kind + ":" + name
	if use, ok := h.triggered[key]; ok {
		use.Calls++
		return
	}
	h.triggered[key] = &TriggeredContext{Kind: kind, Name: name, Calls: 1}
	h.triggeredOrder = append(h.triggeredOrder, key)
}

func (h *heavyDetail) addFileEdit(path string, additions, deletions int, isNew bool, hunks []string) {
	if path == "" {
		return
	}
	if edit, ok := h.fileEdits[path]; ok {
		edit.Additions += additions
		edit.Deletions += deletions
		edit.Edits++
		edit.IsNew = edit.IsNew || isNew
		edit.Hunks = append(edit.Hunks, hunks...)
		return
	}
	h.fileEdits[path] = &FileEdit{Path: path, Additions: additions, Deletions: deletions, Edits: 1, IsNew: isNew, Hunks: append([]string{}, hunks...)}
	h.fileOrder = append(h.fileOrder, path)
}

func (h *heavyDetail) addContext(path string, segment ContextSegment, partial bool, totalLines *int, captured bool, groupID *string) {
	if path == "" {
		return
	}
	file, ok := h.contextFiles[path]
	if !ok {
		file = &ContextFile{Path: path, TotalLines: totalLines, CapturedContent: captured, Segments: []ContextSegment{}}
		h.contextFiles[path] = file
		h.contextOrder = append(h.contextOrder, path)
	}
	file.Partial = file.Partial || partial
	file.CapturedContent = file.CapturedContent || captured
	if totalLines != nil {
		value := *totalLines
		file.TotalLines = &value
	}
	if groupID != nil {
		value := *groupID
		file.CombinedReadID = &value
	}
	if segment.Content != "" || captured {
		file.Segments = append(file.Segments, segment)
	}
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func diffLines(lines []string) (int, int) {
	adds, dels := 0, 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			adds++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			dels++
		}
	}
	return adds, dels
}

func countLines(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

func stringField(value any, key string) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	text, _ := object[key].(string)
	return text
}

func mapField(value any, key string) map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	child, _ := object[key].(map[string]any)
	return child
}

func arrayField(value any, key string) []any {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	items, _ := object[key].([]any)
	return items
}

func boolField(value any, key string) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	result, _ := object[key].(bool)
	return result
}

func intField(value any, key string) (int, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	number, ok := object[key].(float64)
	return int(number), ok
}

func decodeObject(raw json.RawMessage) (map[string]any, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	return normalizeNumbers(object).(map[string]any), nil
}

func normalizeNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		number, _ := typed.Float64()
		return number
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeNumbers(child)
		}
	case []any:
		for index, child := range typed {
			typed[index] = normalizeNumbers(child)
		}
	}
	return value
}

func (h *heavyDetail) finish(base session.Session) *Detail {
	sort.Ints(h.turnOrder)
	turns := make([]Turn, 0, len(h.turnOrder))
	for _, index := range h.turnOrder {
		turn := *h.turns[index]
		turn.FileEdits = dedupe(turn.FileEdits)
		turns = append(turns, turn)
	}
	files := make([]FileEdit, 0, len(h.fileOrder))
	for _, path := range h.fileOrder {
		files = append(files, *h.fileEdits[path])
	}
	contexts := make([]ContextFile, 0, len(h.contextOrder))
	for _, path := range h.contextOrder {
		contextFile := *h.contextFiles[path]
		sort.SliceStable(contextFile.Segments, func(i, j int) bool { return contextFile.Segments[i].StartLine < contextFile.Segments[j].StartLine })
		contexts = append(contexts, contextFile)
	}
	triggered := make([]TriggeredContext, 0, len(h.triggeredOrder))
	for _, key := range h.triggeredOrder {
		triggered = append(triggered, *h.triggered[key])
	}
	return &Detail{Session: base, TurnLog: turns, FileEdits: files, ContextFiles: contexts, ContextReadGroups: h.readGroups, DeferredContext: h.deferred, TriggeredContext: triggered}
}

func dedupe(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
