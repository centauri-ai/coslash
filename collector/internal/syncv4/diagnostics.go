package syncv4

import (
	"encoding/json"
	"os"
	"os/user"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

var diagnosticsAccountOnce sync.Once
var diagnosticsAccount hubclient.DiagnosticsAccount

func currentDiagnosticsAccount() hubclient.DiagnosticsAccount {
	diagnosticsAccountOnce.Do(func() {
		home, _ := os.UserHomeDir()
		diagnosticsAccount = resolveDiagnosticsAccount(os.Geteuid(), home, user.LookupId, user.Current)
	})
	return diagnosticsAccount
}

func resolveDiagnosticsAccount(id int, home string, lookup func(string) (*user.User, error), current func() (*user.User, error)) hubclient.DiagnosticsAccount {
	var account hubclient.DiagnosticsAccount
	var u *user.User
	var err error
	if id >= 0 {
		u, err = lookup(strconv.Itoa(id))
	} else {
		u, err = current()
	}
	if err == nil && u != nil {
		account.Name = safeAccountName(u.Username)
	}
	if home != "" {
		value := "~"
		account.Home = &value
	}
	return account
}

func safeAccountName(name string) *string {
	if name == "" || len(name) > 128 || !utf8.ValidString(name) {
		return nil
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return nil
		}
	}
	return &name
}

// SetDiagnosticsRoots saves the latest scheduled scan result in memory only.
func (q *Queue) SetDiagnosticsRoots(roots map[string]string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.diagnosticsRoots = make(map[string]string, len(roots))
	for path, state := range roots {
		q.diagnosticsRoots[path] = state
	}
}

func (q *Queue) localDiagnostics(now time.Time) *hubclient.LocalDiagnostics {
	q.mu.Lock()
	if !q.diagnosticsAdvertised {
		q.mu.Unlock()
		return nil
	}
	account := q.diagnosticsAccount
	q.mu.Unlock()
	if account == nil {
		account = currentDiagnosticsAccount
	}
	identity := account()
	q.mu.Lock()
	if !q.diagnosticsAdvertised {
		q.mu.Unlock()
		return nil
	}
	d := &hubclient.LocalDiagnostics{Version: 1, Account: identity, State: "unknown", Agents: []hubclient.DiagnosticsAgent{}}
	names := []string{"claude", "codex", "cursor", "grok", "opencode", "pi"}
	roots := map[string][]string{"claude": {"~/.claude/projects"}, "codex": {"~/.codex/archived_sessions", "~/.codex/sessions"}}
	for _, name := range names {
		a := hubclient.DiagnosticsAgent{Agent: name, Roots: []hubclient.DiagnosticsRoot{}}
		for _, path := range roots[name] {
			state := q.diagnosticsRoots[path]
			if state != "ok" && state != "missing" && state != "unreadable" {
				state = "unknown"
			}
			a.Roots = append(a.Roots, hubclient.DiagnosticsRoot{Path: path, State: state})
		}
		d.Agents = append(d.Agents, a)
	}
	plan := q.state.Config.ImportPlan
	if plan != nil && plan.Version > 0 && plan.Version <= 2147483647 {
		version := plan.Version
		d.PlanVersion = &version
		if validateImportPlan(*plan) == nil && q.state.CatchUpFrozenVersion == plan.Version && q.state.PlanStartedAt > 0 {
			d.State = "partial"
			startedAt := time.UnixMilli(q.state.PlanStartedAt)
			selected, deleted, quiet := make([]int, len(names)), make([]int, len(names)), make([]int, len(names))
			uncertain := make([]bool, len(names))
			for _, entry := range q.state.Entries {
				if !q.inPlanScopeLocked(entry, *plan, startedAt) {
					continue
				}
				i := slices.Index(names, entry.Session.Agent)
				if i < 0 {
					continue
				}
				if entry.Selection.SourceKind != "local" {
					if entry.Selection.SourceKind != "ssh" {
						uncertain[i] = true
					}
					continue
				}
				selected[i]++
				if entry.FailureCode == "session_deleted" {
					deleted[i]++
				} else if pending(entry) && !readyLive(entry, now) {
					quiet[i]++
				}
			}
			for i := range d.Agents {
				a := &d.Agents[i]
				if !uncertain[i] && int64(selected[i]) <= 2147483647 {
					a.Selected, a.Reasons.Deleted, a.Reasons.Quiet = &selected[i], &deleted[i], &quiet[i]
				}
			}
		}
	}
	q.mu.Unlock()
	encoded, err := json.Marshal(d)
	if err != nil || len(encoded) > 8192 {
		return nil
	}
	return d
}
