// Package diagnostics reports local coSlash readiness without hiding probe failures.
package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

const maxSkippedPaths = 10

type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

type SourceState string

const (
	SourceOK         SourceState = "ok"
	SourceEmpty      SourceState = "empty"
	SourceMissing    SourceState = "missing"
	SourceUnreadable SourceState = "unreadable"
)

type Snapshot struct {
	Version     string    `json:"version"`
	GeneratedAt int64     `json:"generatedAt"`
	Platform    Platform  `json:"platform"`
	Storage     Storage   `json:"storage"`
	Synthesis   Synthesis `json:"synthesis"`
	Settings    any       `json:"settings"`
	Sources     []Source  `json:"sources"`
	Checks      []Check   `json:"checks"`
	countsError string
}

type Platform struct {
	OS                      string `json:"os"`
	Arch                    string `json:"arch"`
	TerminalLaunchSupported bool   `json:"terminalLaunchSupported"`
}

type Storage struct {
	Home      string `json:"home"`
	Writable  bool   `json:"writable"`
	Summaries int    `json:"summaries"`
	Error     string `json:"error"`
}

type Synthesis struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model"`
	CLI      string `json:"cli"`
	CLIFound bool   `json:"cliFound"`
	Reason   string `json:"reason"`
}

type Source struct {
	Agent        string        `json:"agent"`
	Label        string        `json:"label"`
	Root         string        `json:"root"`
	State        SourceState   `json:"state"`
	Transcripts  int           `json:"transcripts"`
	Sessions     int           `json:"sessions"`
	Skipped      []SkippedPath `json:"skipped"`
	SkippedTotal int           `json:"skippedTotal"`
	Error        string        `json:"error"`
	CLI          CLI           `json:"cli"`
}

type SkippedPath struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type CLI struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

type Deps struct {
	Sources          func() []collector.SourceHealth
	SessionCounts    func() (map[string]int, error)
	LookPath         func(string) (string, error)
	CLIVersion       func(context.Context, string) string
	Home             func() string
	UserHome         func() (string, error)
	GOOS             string
	GOARCH           string
	Version          string
	Now              func() time.Time
	SynthesisEnabled bool
	SynthesisModel   string
	SynthesisCLI     string
}

func Default(version string) Deps {
	state := settings.Open().State()
	config := state.Config.Synthesis
	return Deps{
		Sources:          collector.Sources,
		SessionCounts:    collector.SessionCountsByAgent,
		LookPath:         exec.LookPath,
		CLIVersion:       commandVersion,
		Home:             synthesis.Home,
		UserHome:         os.UserHomeDir,
		GOOS:             runtime.GOOS,
		GOARCH:           runtime.GOARCH,
		Version:          version,
		Now:              time.Now,
		SynthesisEnabled: state.Valid && state.Persisted && config.Enabled,
		SynthesisModel:   config.Model,
		SynthesisCLI:     settings.BackendExecutable(config.Backend),
	}
}

// Collect turns every probe failure into data so the diagnostic surface itself remains available.
func Collect(ctx context.Context, deps Deps) *Snapshot {
	userHome, _ := deps.UserHome()
	snapshot := &Snapshot{
		Version:     deps.Version,
		GeneratedAt: deps.Now().UnixMilli(),
		Platform: Platform{
			OS:                      deps.GOOS,
			Arch:                    deps.GOARCH,
			TerminalLaunchSupported: deps.GOOS == "darwin",
		},
		Settings: nil,
		Sources:  []Source{},
		Checks:   []Check{},
	}

	storageHome := deps.Home()
	snapshot.Storage = probeStorage(storageHome)
	snapshot.Storage.Home = displayPath(userHome, storageHome)
	snapshot.Storage.Error = displayError(userHome, snapshot.Storage.Error)

	counts, countsErr := deps.SessionCounts()
	if countsErr != nil {
		snapshot.countsError = displayError(userHome, countsErr.Error())
		counts = map[string]int{}
	}

	for _, health := range deps.Sources() {
		snapshot.Sources = append(snapshot.Sources, collectSource(ctx, deps, userHome, health, counts))
	}

	synthesisCLIFound := false
	for _, source := range snapshot.Sources {
		if source.CLI.Name == deps.SynthesisCLI {
			synthesisCLIFound = source.CLI.Found
			break
		}
	}
	snapshot.Synthesis = Synthesis{
		Enabled:  deps.SynthesisEnabled,
		Model:    deps.SynthesisModel,
		CLI:      deps.SynthesisCLI,
		CLIFound: synthesisCLIFound,
	}
	if deps.SynthesisEnabled && !synthesisCLIFound {
		snapshot.Synthesis.Reason = fmt.Sprintf("%s CLI is not on PATH.", deps.SynthesisCLI)
	}
	snapshot.Checks = derive(snapshot)
	return snapshot
}

func collectSource(
	ctx context.Context,
	deps Deps,
	userHome string,
	health collector.SourceHealth,
	counts map[string]int,
) Source {
	source := Source{
		Agent:   health.Agent,
		Label:   sourceLabel(health.Agent),
		Root:    displayPath(userHome, health.Root),
		State:   SourceUnreadable,
		Skipped: []SkippedPath{},
		CLI:     CLI{Name: health.Agent},
	}
	if health.ScanErr != nil {
		source.Error = displayError(userHome, health.ScanErr.Error())
	} else if health.Scan != nil {
		source.SkippedTotal = max(health.Scan.SkippedTotal, len(health.Scan.Skipped))
		switch {
		case health.Scan.RootMissing:
			source.State = SourceMissing
		case len(health.Scan.Files) == 0 && source.SkippedTotal > 0:
			source.State = SourceUnreadable
			source.Error = fmt.Sprintf("scan skipped %d unreadable paths", source.SkippedTotal)
			if len(health.Scan.Skipped) > 0 {
				first := health.Scan.Skipped[0]
				source.Error += fmt.Sprintf("; first failure: %s: %s", first.Path, first.Error)
			}
			source.Error = displayError(userHome, source.Error)
		case len(health.Scan.Files) == 0:
			source.State = SourceEmpty
		default:
			source.State = SourceOK
		}
		source.Transcripts = len(health.Scan.Files)
		source.Sessions = counts[health.Agent]
		for _, skipped := range health.Scan.Skipped[:min(len(health.Scan.Skipped), maxSkippedPaths)] {
			source.Skipped = append(source.Skipped, SkippedPath{
				Path:  displayPath(userHome, skipped.Path),
				Error: displayError(userHome, skipped.Error),
			})
		}
	}
	if path, err := deps.LookPath(health.Agent); err == nil {
		source.CLI.Found = true
		source.CLI.Path = displayPath(userHome, path)
		source.CLI.Version = deps.CLIVersion(ctx, path)
	}
	return source
}

func sourceLabel(agent string) string {
	if agent == "claude" {
		return "Claude Code"
	}
	if agent == "codex" {
		return "Codex"
	}
	return agent
}

func probeStorage(home string) Storage {
	storage := Storage{Home: home}
	if err := os.MkdirAll(home, 0o700); err != nil {
		storage.Error = err.Error()
		return storage
	}
	probe, err := os.CreateTemp(home, ".diagnostics-*")
	if err != nil {
		storage.Error = err.Error()
		return storage
	}
	probePath := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(probePath)
	if err := errors.Join(closeErr, removeErr); err != nil {
		storage.Error = err.Error()
		return storage
	}
	storage.Writable = true
	entries, err := os.ReadDir(filepath.Join(home, "summaries"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		storage.Error = err.Error()
		return storage
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			storage.Summaries++
		}
	}
	return storage
}
