package opencode

import (
	"database/sql"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	metadata := vendors.EmptySessionMetadata()
	output, err := exec.Command("ps", "-ww", "-axo", "pid=,lstart=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	processes := parseTUIProcesses(string(output))
	for index := range processes {
		cwd := processWorkingDirectory(processes[index].pid)
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
	candidates, err := loadLiveCandidates(db)
	if err != nil {
		return nil, fmt.Errorf("load live candidates: %w", err)
	}
	for id := range matchLiveSessions(processes, candidates) {
		metadata.Live[id] = "interactive"
	}
	return metadata, nil
}

func parseTUIProcesses(output string) []tuiProcess {
	var processes []tuiProcess
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || filepath.Base(fields[6]) != "opencode" {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		started, err := time.ParseInLocation(
			"Mon Jan 2 15:04:05 2006",
			strings.Join(fields[1:6], " "),
			time.Local,
		)
		if err != nil {
			continue
		}
		project, sessionID, fork, tui := parseTUIArgs(fields[7:])
		if !tui {
			continue
		}
		processes = append(processes, tuiProcess{
			pid: pid, startedAt: started.UnixMilli(), project: project,
			sessionID: sessionID, fork: fork,
		})
	}
	return processes
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

func processWorkingDirectory(pid int) string {
	output, err := exec.Command(
		"lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn",
	).Output()
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n")
		}
	}
	return ""
}

func loadLiveCandidates(db *sql.DB) ([]liveCandidate, error) {
	rows, err := db.Query(`
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
			if matchedSessions[candidate.id] || process.directory != candidate.directory ||
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
