package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const (
	resumedSessionWindow = 3 * time.Minute
)

var sessionIDPattern = regexp.MustCompile(`^ses_[0-9A-Za-z]+$`)

var nonTUICommands = map[string]struct{}{
	"acp": {}, "agent": {}, "attach": {}, "auth": {}, "completion": {}, "db": {},
	"debug": {}, "export": {}, "github": {}, "import": {}, "mcp": {}, "models": {},
	"plugin": {}, "pr": {}, "providers": {}, "run": {}, "serve": {}, "session": {},
	"stats": {}, "uninstall": {}, "upgrade": {}, "web": {},
}

var valueFlags = map[string]struct{}{
	"--agent": {}, "--cors": {}, "--hostname": {}, "--log-level": {}, "--mdns-domain": {},
	"--model": {}, "--port": {}, "--prompt": {}, "--replay-limit": {}, "--session": {},
	"-m": {}, "-s": {},
}

type tuiProcess struct {
	pid       int
	startedAt int64
	directory string
	project   string
	sessionID string
	fork      bool
}

type liveCandidate struct {
	id           string
	directory    string
	createdAt    int64
	userMessages []int64
}

func loadMetadata(db *sql.DB) (*vendors.SessionMetadata, error) {
	return loadMetadataContext(context.Background(), db)
}

func loadMetadataContext(ctx context.Context, db *sql.DB) (*vendors.SessionMetadata, error) {
	metadata := vendors.EmptySessionMetadata()
	processes, err := listTUIProcessesContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	for index := range processes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cwd := processWorkingDirectoryContext(ctx, processes[index].pid)
		if cwd == "" {
			continue
		}
		if project := processes[index].project; project != "" {
			if filepath.IsAbs(project) {
				cwd = project
			} else {
				cwd = filepath.Join(cwd, project)
			}
		}
		processes[index].directory = filepath.Clean(cwd)
	}
	candidates, err := loadLiveCandidatesContext(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("load live candidates: %w", err)
	}
	for id := range matchLiveSessions(processes, candidates) {
		metadata.Session(id).Live = "interactive"
	}
	if err := markPendingPermissionsContext(ctx, db, metadata, permissionStateDir()); err != nil {
		return nil, err
	}
	return metadata, nil
}

type pendingPermission struct {
	SessionID string `json:"sessionID"`
	PID       int    `json:"pid"`
}

type sessionClientRecord struct {
	SessionID string `json:"sessionID"`
	Client    string `json:"client"`
}

func permissionStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".coslash", "opencode-permissions")
}

func clientStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".coslash", "opencode-clients")
}

func sessionEntrypointContext(ctx context.Context, id, directory string) *string {
	if !sessionIDPattern.MatchString(id) || directory == "" {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(directory, id+".json"))
	if err != nil {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	var record sessionClientRecord
	if json.Unmarshal(data, &record) != nil || record.SessionID != id ||
		(record.Client != "desktop" && record.Client != "cli") {
		return nil
	}
	value := "opencode-" + record.Client
	return &value
}

func markPendingPermissionsContext(
	ctx context.Context,
	db *sql.DB,
	metadata *vendors.SessionMetadata,
	directory string,
) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var pending pendingPermission
		if json.Unmarshal(data, &pending) != nil || pending.SessionID == "" || !session.IsProcessAlive(pending.PID) {
			os.Remove(path)
			continue
		}
		var rootID string
		if db.QueryRowContext(ctx,
			`SELECT COALESCE(parent_id, id) FROM session WHERE id = ? AND time_archived IS NULL`,
			pending.SessionID,
		).Scan(&rootID) == nil {
			metadata.Session(rootID).Live = "waiting"
		}
	}
	return nil
}

func parseTUIArgs(args []string) (project, sessionID string, fork, tui bool) {
	tui = true
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "-h" || argument == "--help" || argument == "-v" || argument == "--version":
			tui = false
		case argument == "--fork":
			fork = true
		case argument == "-s" || argument == "--session":
			if index+1 < len(args) {
				index++
				if sessionIDPattern.MatchString(args[index]) {
					sessionID = args[index]
				}
			}
		case strings.HasPrefix(argument, "-s="):
			id := strings.TrimPrefix(argument, "-s=")
			if sessionIDPattern.MatchString(id) {
				sessionID = id
			}
		case strings.HasPrefix(argument, "--session="):
			id := strings.TrimPrefix(argument, "--session=")
			if sessionIDPattern.MatchString(id) {
				sessionID = id
			}
		case strings.Contains(argument, "=") || strings.HasPrefix(argument, "-"):
			if _, takesValue := valueFlags[argument]; takesValue && index+1 < len(args) {
				index++
			}
		default:
			if project == "" {
				project = argument
			}
		}
	}
	if _, excluded := nonTUICommands[project]; excluded {
		project = ""
		tui = false
	}
	return
}

func loadLiveCandidatesContext(ctx context.Context, db *sql.DB) ([]liveCandidate, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT session.id, session.directory, session.time_created, message.time_created
		FROM session
		LEFT JOIN message ON message.session_id = session.id
			AND json_extract(message.data, '$.role') = 'user'
		WHERE session.parent_id IS NULL AND session.time_archived IS NULL
		ORDER BY session.id, message.time_created
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []liveCandidate
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var id, directory string
		var createdAt int64
		var messageAt sql.NullInt64
		if err := rows.Scan(&id, &directory, &createdAt, &messageAt); err != nil {
			return nil, err
		}
		if len(candidates) == 0 || candidates[len(candidates)-1].id != id {
			candidates = append(candidates, liveCandidate{
				id: id, directory: filepath.Clean(directory), createdAt: createdAt,
			})
		}
		if messageAt.Valid {
			last := len(candidates) - 1
			candidates[last].userMessages = append(candidates[last].userMessages, messageAt.Int64)
		}
	}
	return candidates, rows.Err()
}

func matchLiveSessions(processes []tuiProcess, candidates []liveCandidate) map[string]struct{} {
	// OpenCode does not expose in-TUI session switches, so this only infers the
	// launch or first activity and leaves ambiguous process/session pairs inactive.
	live := map[string]struct{}{}
	matchedProcesses := map[int]bool{}
	matchedSessions := map[string]bool{}
	byID := make(map[string]liveCandidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.id] = candidate
	}
	for index, process := range processes {
		if process.fork || process.sessionID == "" {
			continue
		}
		if _, exists := byID[process.sessionID]; !exists {
			continue
		}
		live[process.sessionID] = struct{}{}
		matchedProcesses[index] = true
		matchedSessions[process.sessionID] = true
	}

	matchUnique(processes, candidates, matchedProcesses, matchedSessions, func(
		process tuiProcess, candidate liveCandidate,
	) bool {
		delta := candidate.createdAt - process.startedAt
		return delta >= -time.Second.Milliseconds()
	})
	matchUnique(processes, candidates, matchedProcesses, matchedSessions, func(
		process tuiProcess, candidate liveCandidate,
	) bool {
		if candidate.createdAt >= process.startedAt {
			return false
		}
		for _, messageAt := range candidate.userMessages {
			delta := messageAt - process.startedAt
			if delta >= 0 && delta <= resumedSessionWindow.Milliseconds() {
				return true
			}
		}
		return false
	})
	for id := range matchedSessions {
		live[id] = struct{}{}
	}
	return live
}

func matchUnique(
	processes []tuiProcess,
	candidates []liveCandidate,
	matchedProcesses map[int]bool,
	matchedSessions map[string]bool,
	matches func(tuiProcess, liveCandidate) bool,
) {
	type edge struct {
		process int
		session string
	}
	var edges []edge
	processDegrees := map[int]int{}
	sessionDegrees := map[string]int{}
	for processIndex, process := range processes {
		if matchedProcesses[processIndex] || process.directory == "" {
			continue
		}
		for _, candidate := range candidates {
			if matchedSessions[candidate.id] || !sameDirectory(process.directory, candidate.directory) ||
				!matches(process, candidate) {
				continue
			}
			edges = append(edges, edge{process: processIndex, session: candidate.id})
			processDegrees[processIndex]++
			sessionDegrees[candidate.id]++
		}
	}
	for _, edge := range edges {
		if processDegrees[edge.process] != 1 || sessionDegrees[edge.session] != 1 {
			continue
		}
		matchedProcesses[edge.process] = true
		matchedSessions[edge.session] = true
	}
}
