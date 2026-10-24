package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// shellCall is one shell command from a side store, in conversation order.
// output is nil when the store has no usable result for it.
type shellCall struct {
	command string
	output  *string
}

// applyCommandOutputs fills the output of each recorded shell command from the
// side store of the lane that owns the session. The transcript has no tool
// results and no tool call IDs, so a command gets an output only when the
// store holds the same command text the same number of times. A session whose
// lane is unknown gets nothing.
func applyCommandOutputs(ctx context.Context, home string, parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata) error {
	if metadata == nil {
		return ctx.Err()
	}
	lanes := map[string][]string{}
	for _, item := range parsed {
		if item == nil || item.Session == nil || len(item.Session.Commands) == 0 {
			continue
		}
		if entry := metadata.Lookup(item.Session.ID); entry != nil {
			lanes[entry.Entrypoint] = append(lanes[entry.Entrypoint], item.Session.ID)
		}
	}
	calls := map[string][]shellCall{}
	if ids := lanes[entrypointIDE]; len(ids) > 0 {
		loaded, err := loadIDEShellCalls(ctx, home, ids)
		if err != nil {
			return err
		}
		for id, list := range loaded {
			calls[id] = list
		}
	}
	if ids := lanes[entrypointCLI]; len(ids) > 0 {
		loaded, err := loadCLIShellCalls(ctx, home, ids)
		if err != nil {
			return err
		}
		for id, list := range loaded {
			calls[id] = list
		}
	}
	for _, item := range parsed {
		if item == nil || item.Session == nil || len(item.Session.Commands) == 0 || len(item.Commands) != len(item.Session.Commands) {
			continue
		}
		outputs, found := pairShellOutputs(item.Session.Commands, calls[item.Session.ID])
		if !found {
			continue
		}
		item.Session.CommandOutputs = outputs
		for i := range item.Commands {
			item.Commands[i].Output = outputs[i]
		}
	}
	return ctx.Err()
}

// pairShellOutputs matches commands to calls by exact text. The k-th use of a
// text pairs with the k-th call of that text. When the counts differ the
// pairing is ambiguous, so every use of that text gets nil.
func pairShellOutputs(commands []string, calls []shellCall) ([]*string, bool) {
	want := map[string]int{}
	for _, command := range commands {
		want[command]++
	}
	byText := map[string][]shellCall{}
	for _, call := range calls {
		byText[call.command] = append(byText[call.command], call)
	}
	outputs := make([]*string, len(commands))
	used := map[string]int{}
	found := false
	for i, command := range commands {
		k := used[command]
		used[command]++
		group := byText[command]
		if len(group) != want[command] || group[k].output == nil {
			continue
		}
		output := session.TruncateOutput(*group[k].output)
		outputs[i], found = &output, true
	}
	return outputs, found
}

func loadIDEShellCalls(ctx context.Context, home string, ids []string) (map[string][]shellCall, error) {
	db, err := openCursorDBContext(ctx, filepath.Join(GlobalStorage(home), "state.vscdb"))
	if err != nil {
		return nil, ctx.Err()
	}
	defer db.Close()
	query, args := cursorKeyQuery(`SELECT key, value FROM cursorDiskKV`, "bubbleId:", canonicalCursorIDs(ids))
	rows, err := db.QueryContext(ctx, query+` AND value LIKE '%run_terminal_c%'`, args...)
	if err != nil {
		return nil, ctx.Err()
	}
	defer rows.Close()
	type bubbleCall struct {
		key, createdAt string
		call           shellCall
	}
	bubbles := map[string][]bubbleCall{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var key, value string
		if rows.Scan(&key, &value) != nil {
			continue
		}
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 {
			continue
		}
		var bubble struct {
			CreatedAt      string `json:"createdAt"`
			ToolFormerData struct {
				Name, Status, Result string
				RawArgs, Params      json.RawMessage
			} `json:"toolFormerData"`
		}
		tool := &bubble.ToolFormerData
		if json.Unmarshal([]byte(value), &bubble) != nil {
			continue
		}
		command, ok := terminalCommand(tool.Name, tool.RawArgs, tool.Params)
		// A bubble without a time has no reliable place in the order.
		if _, timed := parseTimestamp(bubble.CreatedAt); !ok || !timed {
			continue
		}
		id := canonicalCursorID(parts[1])
		bubbles[id] = append(bubbles[id], bubbleCall{key, bubble.CreatedAt, shellCall{command, ideShellOutput(tool.Status, tool.Result)}})
	}
	if err := rows.Err(); err != nil {
		return nil, ctx.Err()
	}
	calls := map[string][]shellCall{}
	for id, list := range bubbles {
		sort.SliceStable(list, func(i, j int) bool {
			a, _ := parseTimestamp(list[i].createdAt)
			b, _ := parseTimestamp(list[j].createdAt)
			if !a.Equal(b) {
				return a.Before(b)
			}
			return list[i].key < list[j].key
		})
		for _, item := range list {
			calls[id] = append(calls[id], item.call)
		}
	}
	return calls, ctx.Err()
}

// ideShellOutput decodes a terminal bubble result, a JSON object whose
// "output" holds the text. Cursor drops empty fields, so a finished command
// without "output" printed nothing. A rejected or skipped command never ran.
func ideShellOutput(status, result string) *string {
	if status != "completed" || result == "" {
		return nil
	}
	var value struct {
		Output         *string `json:"output"`
		Rejected       *bool   `json:"rejected"`
		ExitCode       *int    `json:"exitCode"`
		ExitCodeV2     *int    `json:"exitCodeV2"`
		NotInterrupted *bool   `json:"notInterrupted"`
	}
	if json.Unmarshal([]byte(result), &value) != nil || (value.Rejected != nil && *value.Rejected) {
		return nil
	}
	if value.Output != nil {
		return value.Output
	}
	if value.Rejected != nil || value.ExitCode != nil || value.ExitCodeV2 != nil || value.NotInterrupted != nil {
		empty := ""
		return &empty
	}
	return nil
}

func loadCLIShellCalls(ctx context.Context, home string, ids []string) (map[string][]shellCall, error) {
	stores, err := cursorChatStoresContext(ctx, home, canonicalCursorIDs(ids))
	if err != nil {
		return nil, err
	}
	calls := map[string][]shellCall{}
	for _, path := range stores {
		id := canonicalCursorID(filepath.Base(filepath.Dir(path)))
		list, err := readCLIStoreShellCalls(ctx, path)
		if err != nil {
			return nil, err
		}
		calls[id] = append(calls[id], list...)
	}
	return calls, nil
}

func readCLIStoreShellCalls(ctx context.Context, path string) ([]shellCall, error) {
	db, err := openCursorDBContext(ctx, path)
	if err != nil {
		return nil, ctx.Err()
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT data FROM blobs ORDER BY rowid`)
	if err != nil {
		return nil, ctx.Err()
	}
	defer rows.Close()
	var calls []shellCall
	index := map[string]int{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var data []byte
		if rows.Scan(&data) != nil || !bytes.HasPrefix(data, []byte("{")) {
			continue
		}
		var message struct {
			Content []struct {
				Type       string          `json:"type"`
				ToolCallID string          `json:"toolCallId"`
				ToolName   string          `json:"toolName"`
				Args       json.RawMessage `json:"args"`
				Result     json.RawMessage `json:"result"`
			} `json:"content"`
		}
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		for _, block := range message.Content {
			if block.ToolName != "Shell" && block.ToolName != "run_terminal_cmd" {
				continue
			}
			switch block.Type {
			case "tool-call":
				command, ok := terminalCommand("run_terminal_cmd", nil, block.Args)
				if _, seen := index[block.ToolCallID]; ok && !seen {
					index[block.ToolCallID] = len(calls)
					calls = append(calls, shellCall{command: command})
				}
			case "tool-result":
				at, known := index[block.ToolCallID]
				if output, ok := cliShellOutput(block.Result); known && ok {
					calls[at].output = &output
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, ctx.Err()
	}
	return calls, ctx.Err()
}

var cliResultHeader = regexp.MustCompile(`^Exit code: -?\d+\n\nCommand output(?: \(truncated to \d+ characters\))?:\n\n` + "```\n")

const cliResultFooter = "\n```\n\nCommand completed"

// cliShellOutput extracts the command output from the text that the CLI shell
// tool returns. Only the "Exit code" form with a fenced body carries output.
// Other results (blocked, interrupted, sent to the background, output written
// to a file) have none.
func cliShellOutput(raw json.RawMessage) (string, bool) {
	var result string
	if json.Unmarshal(raw, &result) != nil {
		return "", false
	}
	header := cliResultHeader.FindString(result)
	if header == "" {
		return "", false
	}
	body := result[len(header):]
	end := strings.LastIndex(body, cliResultFooter)
	if end < 0 {
		return "", false
	}
	return body[:end], true
}
