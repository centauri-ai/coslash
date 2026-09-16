package review

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

const prefix = "Review — "

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
			m.states[originID] = State{Error: err.Error()}
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
		branch = *origin.Branch
	}
	outcome := "—"
	if origin.Synthesis != nil && origin.Synthesis.Outcome != "" {
		outcome = origin.Synthesis.Outcome
	} else if origin.Summary != nil {
		outcome = *origin.Summary
	}
	files := make([]string, 0, len(origin.FileEdits))
	for _, edit := range origin.FileEdits {
		files = append(files, "- "+edit.Path)
	}
	if len(files) == 0 {
		files = append(files, "- —")
	}
	commits := make([]string, 0, len(origin.Commits))
	for _, commit := range origin.Commits {
		commits = append(commits, "- "+commit)
	}
	if len(commits) == 0 {
		commits = append(commits, "- —")
	}
	return strings.Join([]string{
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
	}, "\n")
}
