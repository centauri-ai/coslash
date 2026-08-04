package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

const (
	SchemaURL = "https://raw.githubusercontent.com/centauri-ai/coslash/main/settings.schema.json"
	Version   = 1

	BackendClaude = "claude-cli"
	BackendCodex  = "codex_exec"

	TerminalApple = "terminal"
	TerminalITerm = "iterm2"
)

type Config struct {
	Schema    string            `json:"$schema"`
	Version   int               `json:"version"`
	Synthesis SynthesisSettings `json:"synthesis"`
	Launch    LaunchSettings    `json:"launch"`
}

type SynthesisSettings struct {
	Enabled bool   `json:"enabled"`
	Backend string `json:"backend"`
	Model   string `json:"model"`
}

type LaunchSettings struct {
	Terminal string `json:"terminal"`
}

type ModelOption struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

type BackendOption struct {
	ID     string        `json:"id"`
	Label  string        `json:"label"`
	Models []ModelOption `json:"models"`
}

type TerminalOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type State struct {
	Config    Config
	Persisted bool
	Valid     bool
	Error     string
}

type Store struct {
	mu     sync.RWMutex
	state  State
	rename func(string, string) error
}

func Home() string {
	if home := os.Getenv("COSLASH_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".coslash"
	}
	return filepath.Join(home, ".coslash")
}

func Path() string {
	return filepath.Join(Home(), "settings.json")
}

func Defaults() Config {
	return Config{
		Schema:  SchemaURL,
		Version: Version,
		Synthesis: SynthesisSettings{
			Enabled: false,
			Backend: BackendClaude,
			Model:   "claude-haiku-4-5",
		},
		Launch: LaunchSettings{Terminal: TerminalApple},
	}
}

func BackendOptions() []BackendOption {
	return []BackendOption{
		{
			ID:    BackendClaude,
			Label: "Claude Code CLI",
			Models: []ModelOption{
				{ID: "claude-haiku-4-5", Label: "Claude Haiku 4.5", Default: true},
				{ID: "claude-sonnet-5", Label: "Claude Sonnet 5"},
				{ID: "claude-opus-5", Label: "Claude Opus 5"},
			},
		},
		{
			ID:    BackendCodex,
			Label: "Codex CLI",
			Models: []ModelOption{
				{ID: "gpt-5.6-luna", Label: "GPT-5.6 Luna", Default: true},
				{ID: "gpt-5.6-terra", Label: "GPT-5.6 Terra"},
				{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol"},
			},
		},
	}
}

func TerminalOptions() []TerminalOption {
	return []TerminalOption{
		{ID: TerminalApple, Label: "Apple Terminal"},
		{ID: TerminalITerm, Label: "iTerm2"},
	}
}

func BackendExecutable(backend string) string {
	switch backend {
	case BackendClaude:
		return "claude"
	case BackendCodex:
		return "codex"
	default:
		return ""
	}
}

func Validate(config Config) error {
	if config.Schema != SchemaURL {
		return fmt.Errorf("$schema must be %q", SchemaURL)
	}
	if config.Version != Version {
		return fmt.Errorf("unsupported settings version %d", config.Version)
	}
	var backend *BackendOption
	for _, option := range BackendOptions() {
		if option.ID == config.Synthesis.Backend {
			selected := option
			backend = &selected
			break
		}
	}
	if backend == nil {
		return fmt.Errorf("unsupported synthesis backend %q", config.Synthesis.Backend)
	}
	modelSupported := false
	for _, option := range backend.Models {
		if option.ID == config.Synthesis.Model {
			modelSupported = true
			break
		}
	}
	if !modelSupported {
		return fmt.Errorf("model %q is not supported by %q", config.Synthesis.Model, backend.ID)
	}
	for _, option := range TerminalOptions() {
		if option.ID == config.Launch.Terminal {
			return nil
		}
	}
	return fmt.Errorf("unsupported terminal %q", config.Launch.Terminal)
}

func Decode(data []byte) (Config, error) {
	type synthesisDocument struct {
		Enabled *bool   `json:"enabled"`
		Backend *string `json:"backend"`
		Model   *string `json:"model"`
	}
	type launchDocument struct {
		Terminal *string `json:"terminal"`
	}
	type configDocument struct {
		Schema    *string            `json:"$schema"`
		Version   *int               `json:"version"`
		Synthesis *synthesisDocument `json:"synthesis"`
		Launch    *launchDocument    `json:"launch"`
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document configDocument
	if err := decoder.Decode(&document); err != nil {
		return Config{}, fmt.Errorf("decode settings: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Config{}, err
	}
	if document.Schema == nil || document.Version == nil || document.Synthesis == nil ||
		document.Synthesis.Enabled == nil || document.Synthesis.Backend == nil ||
		document.Synthesis.Model == nil || document.Launch == nil || document.Launch.Terminal == nil {
		return Config{}, errors.New("settings must include $schema, version, synthesis, and launch fields")
	}
	config := Config{
		Schema:  *document.Schema,
		Version: *document.Version,
		Synthesis: SynthesisSettings{
			Enabled: *document.Synthesis.Enabled,
			Backend: *document.Synthesis.Backend,
			Model:   *document.Synthesis.Model,
		},
		Launch: LaunchSettings{Terminal: *document.Launch.Terminal},
	}
	if err := Validate(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode settings: %w", err)
	}
	return errors.New("decode settings: multiple JSON values")
}

func Open() *Store {
	return &Store{state: load(), rename: os.Rename}
}

func load() State {
	info, err := os.Lstat(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return State{Config: Defaults(), Valid: true}
	}
	if err != nil {
		return State{Config: Defaults(), Persisted: true, Error: "read settings.json: " + err.Error()}
	}
	if !info.Mode().IsRegular() {
		return State{Config: Defaults(), Persisted: true, Error: "settings.json must be a regular file"}
	}
	if info.Mode().Perm() != 0o600 {
		return State{Config: Defaults(), Persisted: true, Error: "settings.json permissions must be 0600"}
	}
	data, err := os.ReadFile(Path())
	if err != nil {
		return State{Config: Defaults(), Persisted: true, Error: "read settings.json: " + err.Error()}
	}
	config, err := Decode(data)
	if err != nil {
		return State{
			Config:    Defaults(),
			Persisted: true,
			Error:     "settings.json is invalid: " + err.Error(),
		}
	}
	return State{Config: config, Persisted: true, Valid: true}
}

func (store *Store) State() State {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.state
}

func (store *Store) Save(config Config) error {
	if err := Validate(config); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(Home(), 0o700); err != nil {
		return fmt.Errorf("create coSlash directory: %w", err)
	}
	if err := os.Chmod(Home(), 0o700); err != nil {
		return fmt.Errorf("secure coSlash directory: %w", err)
	}
	temp, err := os.CreateTemp(Home(), ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create settings temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure settings temporary file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write settings temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync settings temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close settings temporary file: %w", err)
	}
	if err := store.rename(tempPath, Path()); err != nil {
		return fmt.Errorf("replace settings.json: %w", err)
	}
	store.mu.Lock()
	store.state = State{Config: config, Persisted: true, Valid: true}
	store.mu.Unlock()
	return nil
}
