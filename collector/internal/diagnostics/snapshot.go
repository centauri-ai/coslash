// Package diagnostics reports local coSlash readiness without hiding probe failures.
package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

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
	Home     string `json:"home"`
	Writable bool   `json:"writable"`
	Error    string `json:"error"`
}

type Synthesis struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model"`
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

// Collect turns every probe failure into data so the diagnostic surface itself remains available.
func Collect(ctx context.Context, version string) *Snapshot {
	userHome, _ := os.UserHomeDir()
	state := settings.Open().State()
	config := state.Config.Synthesis
	snapshot := &Snapshot{
		Version:     version,
		GeneratedAt: time.Now().UnixMilli(),
		Platform: Platform{
			OS:                      runtime.GOOS,
			Arch:                    runtime.GOARCH,
			TerminalLaunchSupported: runtime.GOOS == "darwin",
		},
		Sources: []Source{},
		Checks:  []Check{},
	}

	storageHome := synthesis.Home()
	snapshot.Storage = probeStorage(storageHome)
	snapshot.Storage.Home = displayPath(userHome, storageHome)
	snapshot.Storage.Error = displayError(userHome, snapshot.Storage.Error)

	counts, countsErr := collector.SessionCountsByAgent()
	if countsErr != nil {
		snapshot.countsError = displayError(userHome, countsErr.Error())
		counts = map[string]int{}
	}

	for _, health := range collector.Sources() {
		snapshot.Sources = append(snapshot.Sources, collectSource(ctx, userHome, health, counts))
	}

	synthesisCLI := settings.BackendExecutable(config.Backend)
	_, synthesisCLIErr := exec.LookPath(synthesisCLI)
	snapshot.Synthesis = Synthesis{
		Enabled:  state.Valid && state.Persisted && config.Enabled,
		Model:    config.Model,
		CLIFound: synthesisCLIErr == nil,
	}
	if snapshot.Synthesis.Enabled && synthesisCLIErr != nil {
		snapshot.Synthesis.Reason = fmt.Sprintf("%s CLI is not on PATH.", synthesisCLI)
	}
	snapshot.Checks = derive(snapshot)
	return snapshot
}

func collectSource(
	ctx context.Context,
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
		for _, skipped := range health.Scan.Skipped {
			source.Skipped = append(source.Skipped, SkippedPath{
				Path:  displayPath(userHome, skipped.Path),
				Error: displayError(userHome, skipped.Error),
			})
		}
	}
	if path, err := exec.LookPath(health.Agent); err == nil {
		source.CLI.Found = true
		source.CLI.Path = displayPath(userHome, path)
		source.CLI.Version = commandVersion(ctx, path)
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
	return storage
}
