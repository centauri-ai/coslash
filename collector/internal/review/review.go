package review

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const (
	prefix              = "Review — "
	failureMessage      = "Review failed. Check the reviewer CLI and try again."
	maxOriginNameRunes  = 120
	maxBranchRunes      = 200
	maxOutcomeRunes     = 4_000
	maxArtifactRunes    = 500
	maxChangeBytes      = 1_500
	maxChangesPerFile   = 5
	maxFiles            = 30
	maxCommits          = 50
	maxPromptBytes      = 16 * 1024
	promptTruncatedMark = "\n…(truncated)"
)

var namePattern = regexp.MustCompile(`^Review — .+ \([^()]{8}\)$`)

type Launch struct {
	Reviewer         string
	WorkingDirectory string
	Name             string
	Prompt           string
}

type State struct {
	Pending bool
	Error   string
}

type Manager struct {
	mu      sync.Mutex
	states  map[string]State
	run     func(context.Context, Launch) error
	timeout time.Duration
}

func NewManager(run func(context.Context, Launch) error) *Manager {
	return &Manager{states: make(map[string]State), run: run, timeout: 30 * time.Minute}
}

func Key(agent, id string) string {
	return agent + ":" + id
}

func (m *Manager) Start(originID string, launch Launch) bool {
	m.mu.Lock()
	if m.states[originID].Pending {
		m.mu.Unlock()
		return false
	}
	m.states[originID] = State{Pending: true}
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()
		err := m.run(ctx, launch)
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil {
			log.Printf("review failed for session %s: %v", originID, err)
			m.states[originID] = State{Error: failureMessage}
		} else {
			delete(m.states, originID)
		}
	}()
	return true
}

func (m *Manager) Status(originID string) State {
	if m == nil {
		return State{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[originID]
}

func Name(originName, originID string) string {
	originName = session.Truncate(originName, maxOriginNameRunes)
	if originName == "" {
		originName = "Untitled session"
	}
	shortID := originID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("%s%s (%s)", prefix, originName, shortID)
}

func NameFromPrompt(prompt string) (string, bool) {
	name, _, _ := strings.Cut(prompt, "\n")
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return "", false
	}
	return name, true
}

func Prompt(origin *session.Session) string {
	name := ""
	if origin.Name != nil {
		name = *origin.Name
	}
	branch := "—"
	if origin.Branch != nil {
		branch = session.Truncate(*origin.Branch, maxBranchRunes)
	}
	outcome := "—"
	if origin.Synthesis != nil && origin.Synthesis.Outcome != "" {
		outcome = session.Truncate(origin.Synthesis.Outcome, maxOutcomeRunes)
	} else if origin.Summary != nil {
		outcome = session.Truncate(*origin.Summary, maxOutcomeRunes)
	}
	files := make([]string, 0, min(len(origin.FileEdits), maxFiles))
	for index, edit := range origin.FileEdits {
		if index == maxFiles {
			files = append(files, "- …(truncated)")
			break
		}
		entry := "- " + session.Truncate(edit.Path, maxArtifactRunes)
		for changeIndex, change := range edit.Changes() {
			if changeIndex == maxChangesPerFile {
				entry += "\n  …(more changes truncated)"
				break
			}
			if text := strings.TrimSpace(change.Text); text != "" {
				entry += "\n  " + change.Operation + ":\n" + limitBytes(text, maxChangeBytes)
			}
		}
		files = append(files, entry)
	}
	if len(files) == 0 {
		files = append(files, "- —")
	}
	commits := make([]string, 0, min(len(origin.Commits), maxCommits))
	for index, commit := range origin.Commits {
		if index == maxCommits {
			commits = append(commits, "- …(truncated)")
			break
		}
		commits = append(commits, "- "+session.Truncate(commit, maxArtifactRunes))
	}
	if len(commits) == 0 {
		commits = append(commits, "- —")
	}
	return limitBytes(strings.Join([]string{
		Name(name, origin.ID),
		"",
		"Review the current working-tree changes. Use an installed code-review skill or your native review capability when available. Do not modify files.",
		"Report findings by severity with file locations, then give a concise conclusion.",
		"",
		"Branch: " + branch,
		"",
		"Debrief:",
		outcome,
		"",
		"Changed files:",
		strings.Join(files, "\n"),
		"",
		"Commits:",
		strings.Join(commits, "\n"),
	}, "\n"), maxPromptBytes)
}

func limitBytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	cut := maximum - len(promptTruncatedMark)
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut]) + promptTruncatedMark
}
