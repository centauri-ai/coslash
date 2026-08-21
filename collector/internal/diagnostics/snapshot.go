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
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
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
	Version             string    `json:"version"`
	GeneratedAt         int64     `json:"generatedAt"`
	Platform            Platform  `json:"platform"`
	Storage             Storage   `json:"storage"`
	Synthesis           Synthesis `json:"synthesis"`
	Sources             []Source  `json:"sources"`
	Remote              *Remote   `json:"remote,omitempty"`
	Checks              []Check   `json:"checks"`
	openCodePlugin      opencode.WaitingPluginHealth
	openCodePluginError string
	homeError           string
}

type Remote struct {
	SourceID         string  `json:"sourceId"`
	Label            string  `json:"label"`
	State            string  `json:"state"`
	Complete         bool    `json:"complete"`
	Reason           *string `json:"reason,omitempty"`
	LastSuccessAtMs  *int64  `json:"lastSuccessAtMs,omitempty"`
	CoverageSinceMs  *int64  `json:"coverageSinceMs,omitempty"`
	RoundTripMs      *int64  `json:"roundTripMs,omitempty"`
	Error            string  `json:"error,omitempty"`
	DiagnosticStderr string  `json:"diagnosticStderr,omitempty"`
}

type RemoteHealth struct {
	SourceID         string
	Label            string
	State            string
	Complete         bool
	Reason           *string
	LastSuccessAtMs  *int64
	CoverageSinceMs  *int64
	RoundTripMs      *int64
	Error            string
	DiagnosticStderr string
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
	Error    string `json:"error"`
}

type Source struct {
	Agent        string        `json:"agent"`
	Label        string        `json:"label"`
	Root         string        `json:"root"`
	State        SourceState   `json:"state"`
	Entries      int           `json:"entries"`
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
func Collect(ctx context.Context, version string, includeVersions bool) *Snapshot {
	return CollectWithRemote(ctx, version, includeVersions, nil)
}

func CollectWithRemote(
	ctx context.Context,
	version string,
	includeVersions bool,
	remoteHealth *RemoteHealth,
) *Snapshot {
	snapshot := collectLocal(ctx, version, includeVersions)
	if remoteHealth == nil || remoteHealth.SourceID == "" {
		return snapshot
	}
	snapshot.Remote = &Remote{
		SourceID: remoteHealth.SourceID, Label: remoteHealth.Label,
		State: remoteHealth.State, Complete: remoteHealth.Complete, Reason: remoteHealth.Reason,
		LastSuccessAtMs: remoteHealth.LastSuccessAtMs, CoverageSinceMs: remoteHealth.CoverageSinceMs,
		RoundTripMs: remoteHealth.RoundTripMs, Error: remoteHealth.Error,
		DiagnosticStderr: remoteHealth.DiagnosticStderr,
	}
	snapshot.Checks = append(snapshot.Checks, remoteCheck(snapshot.Remote))
	return snapshot
}

func collectLocal(ctx context.Context, version string, includeVersions bool) *Snapshot {
	userHome, userHomeErr := os.UserHomeDir()
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
	if userHomeErr != nil {
		snapshot.homeError = userHomeErr.Error()
	}
	snapshot.openCodePlugin = opencode.WaitingPluginDiagnostics()
	snapshot.openCodePlugin.Path = displayPath(userHome, snapshot.openCodePlugin.Path)
	if snapshot.openCodePlugin.Err != nil {
		snapshot.openCodePluginError = displayError(userHome, snapshot.openCodePlugin.Err.Error())
	}

	storageHome := settings.Home()
	snapshot.Storage = probeStorage(storageHome)
	snapshot.Storage.Home = displayPath(userHome, storageHome)
	snapshot.Storage.Error = displayError(userHome, snapshot.Storage.Error)

	for _, health := range collector.Sources() {
		snapshot.Sources = append(snapshot.Sources, collectSource(ctx, userHome, health, includeVersions))
	}

	synthesisCLI := settings.BackendExecutable(config.Backend)
	_, synthesisCLIErr := exec.LookPath(synthesisCLI)
	snapshot.Synthesis = Synthesis{
		Enabled:  state.Valid && state.Persisted && config.Enabled,
		Model:    config.Model,
		CLIFound: synthesisCLIErr == nil,
	}
	if !state.Valid {
		snapshot.Synthesis.Error = displayError(userHome, state.Error)
	} else if snapshot.Synthesis.Enabled && synthesisCLIErr != nil {
		snapshot.Synthesis.Reason = fmt.Sprintf("%s CLI is not on PATH.", synthesisCLI)
	}
	snapshot.Checks = derive(snapshot)
	return snapshot
}

func collectSource(
	ctx context.Context,
	userHome string,
	health collector.SourceHealth,
	includeVersion bool,
) Source {
	source := Source{
		Agent:        health.Agent,
		Label:        sourceLabel(health.Agent),
		Root:         displayPath(userHome, health.Root),
		State:        SourceUnreadable,
		Entries:      health.Entries,
		Sessions:     health.Sessions,
		Skipped:      []SkippedPath{},
		SkippedTotal: health.SkippedTotal,
		CLI:          CLI{Name: health.Agent},
	}
	switch {
	case health.Err != nil:
		source.Error = displayError(userHome, health.Err.Error())
	case health.Missing:
		source.State = SourceMissing
	case health.Entries == 0 && health.SkippedTotal > 0:
		source.Error = fmt.Sprintf("source skipped %d unreadable entries", health.SkippedTotal)
		if len(health.Skipped) > 0 {
			source.Error += "; first failure: " + health.Skipped[0].Error
		}
		source.Error = displayError(userHome, source.Error)
	case health.Entries == 0:
		source.State = SourceEmpty
	default:
		source.State = SourceOK
	}
	for _, skipped := range health.Skipped {
		source.Skipped = append(source.Skipped, SkippedPath{
			Path:  displayPath(userHome, skipped.Path),
			Error: displayError(userHome, skipped.Error),
		})
	}
	if path, err := exec.LookPath(health.Agent); err == nil {
		source.CLI.Found = true
		source.CLI.Path = displayPath(userHome, path)
		if includeVersion {
			source.CLI.Version = commandVersion(ctx, path)
		}
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
	if agent == "opencode" {
		return "OpenCode"
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
