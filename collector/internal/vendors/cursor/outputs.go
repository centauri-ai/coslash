package cursor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

// shellCall is one shell command from a side store, in conversation order.
// output is nil when the store has no usable result for it.
type shellCall struct {
	command string
	output  *string
}

// sessionCalls are the shell calls of one session from a side store. CLI blobs
// have no order that survives a backup projection, so a CLI session pairs only
// commands that occur once.
type sessionCalls struct {
	calls   []shellCall
	ordered bool
}

// applyCommandOutputs fills the output of each recorded shell command from the
// live side store of the lane that owns the session. The transcript has no
// tool results and no tool call IDs, so a command gets an output only when the
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
	calls := map[string]sessionCalls{}
	if ids := lanes[entrypointIDE]; len(ids) > 0 {
		loaded, err := loadIDEShellCalls(ctx, home, ids)
		if err != nil {
			return err
		}
		for id, list := range loaded {
			calls[id] = sessionCalls{list, true}
		}
	}
	if ids := lanes[entrypointCLI]; len(ids) > 0 {
		loaded, err := loadCLIShellCalls(ctx, home, ids)
		if err != nil {
			return err
		}
		for id, list := range loaded {
			calls[id] = sessionCalls{list, false}
		}
	}
	setCommandOutputs(parsed, calls)
	return ctx.Err()
}

// applyBackupCommandOutputs is applyCommandOutputs for a backup parse. It reads
// only the rows that the plan froze, never a live database.
func applyBackupCommandOutputs(parsed []*vendors.ParsedSession, metadata *vendors.SessionMetadata, rows map[string][]BackupRows) error {
	if metadata == nil {
		return nil
	}
	calls := map[string]sessionCalls{}
	for _, item := range parsed {
		if item == nil || item.Session == nil || len(item.Session.Commands) == 0 {
			continue
		}
		id := item.Session.ID
		entry := metadata.Lookup(id)
		if entry == nil {
			continue
		}
		var err error
		switch entry.Entrypoint {
		case entrypointIDE:
			calls[id], err = frozenIDEShellCalls(rows[id], id)
		case entrypointCLI:
			calls[id], err = frozenCLIShellCalls(rows[id], id)
		}
		if err != nil {
			return err
		}
	}
	setCommandOutputs(parsed, calls)
	return nil
}

func setCommandOutputs(parsed []*vendors.ParsedSession, calls map[string]sessionCalls) {
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
}

// pairShellOutputs matches commands to calls by exact text. The k-th use of a
// text pairs with the k-th call of that text. When the counts differ, or the
// calls have no order and the text repeats, the pairing is ambiguous, so every
// use of that text gets nil.
func pairShellOutputs(commands []string, source sessionCalls) ([]*string, bool) {
	want := map[string]int{}
	for _, command := range commands {
		want[command]++
	}
	byText := map[string][]shellCall{}
	for _, call := range source.calls {
		byText[call.command] = append(byText[call.command], call)
	}
	outputs := make([]*string, len(commands))
	used := map[string]int{}
	found := false
	for i, command := range commands {
		k := used[command]
		used[command]++
		group := byText[command]
		if len(group) != want[command] || (!source.ordered && len(group) > 1) || group[k].output == nil {
			continue
		}
		output := session.TruncateOutput(*group[k].output)
		outputs[i], found = &output, true
	}
	return outputs, found
}

// bubbleCall is a shell call from an IDE bubble with the data that orders it.
type bubbleCall struct {
	key, createdAt string
	call           shellCall
}

// ideBubbleCall decodes one cursorDiskKV bubble. It reports false for a bubble
// that is not a terminal call, or that has no time and so no reliable place in
// the order.
func ideBubbleCall(key, value string) (string, bubbleCall, bool) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return "", bubbleCall{}, false
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
		return "", bubbleCall{}, false
	}
	command, ok := terminalCommand(tool.Name, tool.RawArgs, tool.Params)
	if _, timed := parseTimestamp(bubble.CreatedAt); !ok || !timed {
		return "", bubbleCall{}, false
	}
	return canonicalCursorID(parts[1]), bubbleCall{key, bubble.CreatedAt, shellCall{command, ideShellOutput(tool.Status, tool.Result)}}, true
}

func orderedShellCalls(list []bubbleCall) []shellCall {
	sort.SliceStable(list, func(i, j int) bool {
		a, _ := parseTimestamp(list[i].createdAt)
		b, _ := parseTimestamp(list[j].createdAt)
		if !a.Equal(b) {
			return a.Before(b)
		}
		return list[i].key < list[j].key
	})
	calls := make([]shellCall, len(list))
	for i, item := range list {
		calls[i] = item.call
	}
	return calls
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
	bubbles := map[string][]bubbleCall{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var key, value string
		if rows.Scan(&key, &value) != nil {
			continue
		}
		if id, call, ok := ideBubbleCall(key, value); ok {
			bubbles[id] = append(bubbles[id], call)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, ctx.Err()
	}
	calls := map[string][]shellCall{}
	for id, list := range bubbles {
		calls[id] = orderedShellCalls(list)
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
	rows, err := db.QueryContext(ctx, `SELECT data FROM blobs`)
	if err != nil {
		return nil, ctx.Err()
	}
	defer rows.Close()
	reader := cliCallReader{index: map[string]int{}}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var data []byte
		if rows.Scan(&data) == nil {
			reader.add(data)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, ctx.Err()
	}
	return reader.calls, ctx.Err()
}

// cliCallReader collects the shell calls of one CLI store from its blobs.
type cliCallReader struct {
	calls []shellCall
	index map[string]int
}

func (reader *cliCallReader) add(data []byte) {
	if !bytes.HasPrefix(data, []byte("{")) {
		return
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
		return
	}
	for _, block := range message.Content {
		if block.ToolName != "Shell" && block.ToolName != "run_terminal_cmd" {
			continue
		}
		switch block.Type {
		case "tool-call":
			command, ok := terminalCommand("run_terminal_cmd", nil, block.Args)
			if _, seen := reader.index[block.ToolCallID]; ok && !seen {
				reader.index[block.ToolCallID] = len(reader.calls)
				reader.calls = append(reader.calls, shellCall{command: command})
			}
		case "tool-result":
			at, known := reader.index[block.ToolCallID]
			if output, ok := cliShellOutput(block.Result); known && ok {
				reader.calls[at].output = &output
			}
		}
	}
}

// frozenTable returns the rows of one table from the projections of a member.
func frozenTable(rows []BackupRows, sourceKey, table, id string) (sessionbackupv1.DatabaseTable, bool, error) {
	for _, projection := range rows {
		if projection.SourceKey != sourceKey {
			continue
		}
		decoded, err := sessionbackupv1.DecodeDatabaseRows(projection.Bytes, id)
		if err != nil {
			return sessionbackupv1.DatabaseTable{}, false, vendors.ErrInvalidData
		}
		for _, candidate := range decoded.Tables {
			if candidate.Name == table {
				return candidate, true, nil
			}
		}
	}
	return sessionbackupv1.DatabaseTable{}, false, nil
}

func frozenIDEShellCalls(rows []BackupRows, id string) (sessionCalls, error) {
	table, ok, err := frozenTable(rows, "ide-state", "cursorDiskKV", id)
	if err != nil || !ok {
		return sessionCalls{}, err
	}
	key, value := slices.Index(table.Columns, "key"), slices.Index(table.Columns, "value")
	var bubbles []bubbleCall
	for _, row := range table.Rows {
		if key < 0 || value < 0 || len(row.Values) != len(table.Columns) {
			return sessionCalls{}, vendors.ErrInvalidData
		}
		if bubbleID, call, ok := ideBubbleCall(row.Values[key].Value, row.Values[value].Value); ok && bubbleID == id {
			bubbles = append(bubbles, call)
		}
	}
	return sessionCalls{orderedShellCalls(bubbles), true}, nil
}

func frozenCLIShellCalls(rows []BackupRows, id string) (sessionCalls, error) {
	table, ok, err := frozenTable(rows, "cli-store", "blobs", id)
	if err != nil || !ok {
		return sessionCalls{}, err
	}
	data := slices.Index(table.Columns, "data")
	reader := cliCallReader{index: map[string]int{}}
	for _, row := range table.Rows {
		if data < 0 || len(row.Values) != len(table.Columns) {
			return sessionCalls{}, vendors.ErrInvalidData
		}
		switch value := row.Values[data]; value.Type {
		case "text":
			reader.add([]byte(value.Value))
		case "blob":
			if decoded, err := base64.StdEncoding.DecodeString(value.Value); err == nil {
				reader.add(decoded)
			}
		}
	}
	return sessionCalls{reader.calls, false}, nil
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

// loadCLIPullRequestsContext records the pull requests that a CLI session
// created. The transcript has no tool results, so it reads the store blobs. A
// Shell result holds its own command, because its tool call ID differs from the
// ID of the call, so one blob holds the command and the URL, and SQL skips the
// other blobs. Only a command that exited with zero counts.
func loadCLIPullRequestsContext(ctx context.Context, metadata *vendors.SessionMetadata, path string) error {
	id := canonicalCursorID(filepath.Base(filepath.Dir(path)))
	if !transcriptIDPattern.MatchString(id) {
		return nil
	}
	db, err := openCursorDBContext(ctx, path)
	if err != nil {
		return ctx.Err()
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT data FROM blobs
		WHERE instr(CAST(data AS BLOB), CAST('/pull/' AS BLOB)) > 0 AND instr(CAST(data AS BLOB), CAST('pr create' AS BLOB)) > 0`)
	if err != nil {
		return ctx.Err()
	}
	defer rows.Close()
	urls := map[string]struct{}{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var data []byte
		if rows.Scan(&data) != nil {
			continue
		}
		for _, url := range cliPullRequestURLs(data) {
			urls[url] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return ctx.Err()
	}
	if links := session.NewPullRequestLinks(urls); len(links) > 0 {
		entry := metadata.Session(id)
		entry.PullRequestLinks = session.MergePullRequestLinks(entry.PullRequestLinks, links)
		entry.PullRequests = len(entry.PullRequestLinks)
	}
	return ctx.Err()
}

// cliPullRequestURLs returns the URLs that one store blob holds as the output of
// a successful `gh pr create` Shell call.
func cliPullRequestURLs(data []byte) []string {
	if !bytes.HasPrefix(data, []byte("{")) {
		return nil
	}
	var message struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			ToolName string `json:"toolName"`
			Result   string `json:"result"`
		} `json:"content"`
		ProviderOptions struct {
			Cursor struct {
				Result struct {
					Output struct {
						Success struct {
							Command string `json:"command"`
						} `json:"success"`
					} `json:"output"`
				} `json:"highLevelToolCallResult"`
			} `json:"cursor"`
		} `json:"providerOptions"`
	}
	if json.Unmarshal(data, &message) != nil || message.Role != "tool" {
		return nil
	}
	command := message.ProviderOptions.Cursor.Result.Output.Success.Command
	if !session.IsPullRequestCreate(command) {
		return nil
	}
	var urls []string
	for _, block := range message.Content {
		if block.Type == "tool-result" && block.ToolName == "Shell" && strings.HasPrefix(block.Result, "Exit code: 0\n") {
			urls = append(urls, session.CreatedPullRequestURLs(false, block.Result)...)
		}
	}
	return urls
}
