package settings

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	SchemaURL = "https://raw.githubusercontent.com/centauri-ai/coslash/main/settings.schema.json"
	Version   = 1

	BackendClaude   = "claude-cli"
	BackendCodex    = "codex_exec"
	BackendOpenCode = "opencode"

	// Passes no model, leaving OpenCode to resolve one: its configured model
	// key, else the model last selected in the CLI, which varies between runs.
	OpenCodeDefaultModel = "default"

	// Each backend's own default model, which the runner turns up to high
	// reasoning effort. Changing one of these ids moves that flag with it.
	OpenCodeSynthesisModel = "opencode/deepseek-v4-flash-free"
	CodexSynthesisModel    = "gpt-5.6-luna"
	ClaudeSynthesisModel   = "claude-haiku-4-5"

	TerminalApple = "terminal"
	TerminalITerm = "iterm2"
)

type Config struct {
	Schema     string             `json:"$schema"`
	Version    int                `json:"version"`
	Synthesis  SynthesisSettings  `json:"synthesis"`
	Appearance AppearanceSettings `json:"appearance"`
	Launch     LaunchSettings     `json:"launch"`
	Remote     *RemoteSettings    `json:"remote,omitempty"`
}

// RemoteSettings is the optional one-host SSH configuration.
type RemoteSettings struct {
	ID          string             `json:"id"`
	SSHAlias    string             `json:"sshAlias"`
	Enabled     bool               `json:"enabled"`
	Executables *RemoteExecutables `json:"executables,omitempty"`
}

// RemoteExecutables holds optional absolute or home-relative executable
// overrides. Empty values are deliberately not persisted.
type RemoteExecutables struct {
	Claude   string `json:"claude,omitempty"`
	Codex    string `json:"codex,omitempty"`
	OpenCode string `json:"opencode,omitempty"`
}

type SynthesisSettings struct {
	Enabled bool   `json:"enabled"`
	Backend string `json:"backend"`
	Model   string `json:"model"`
}

type LaunchSettings struct {
	Terminal string `json:"terminal"`
}

type AppearanceSettings struct {
	Theme string `json:"theme"`
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
		Appearance: AppearanceSettings{Theme: "light"},
		Launch:     LaunchSettings{Terminal: TerminalApple},
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
		{
			// Filled in from the CLI; OpenCode models depend on the user's providers.
			ID:     BackendOpenCode,
			Label:  "OpenCode CLI",
			Models: []ModelOption{},
		},
	}
}

// A leading "-" is excluded, or the CLI would read the value as another flag.
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

var (
	sshAliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	remoteIDPattern = regexp.MustCompile(`^r_[0-9a-f]{16}$`)
)

// ValidSynthesisModel checks shape, not membership of a fixed list: an API
// proxy such as ANTHROPIC_BASE_URL can serve models the picker never lists.
func ValidSynthesisModel(model string) bool {
	return len(model) <= 200 && modelPattern.MatchString(model)
}

// ValidSSHAlias reports whether alias is safe as a local ssh argv after `--`.
func ValidSSHAlias(alias string) bool {
	return len(alias) > 0 && len(alias) <= 255 && sshAliasPattern.MatchString(alias)
}

// ValidRemoteID reports whether id is a Mac-generated path-safe source id.
func ValidRemoteID(id string) bool {
	return remoteIDPattern.MatchString(id)
}

// ValidRemoteExecutablePath accepts an absolute POSIX path or a leading ~/.
// The remote resolver expands only that leading home marker; it never treats
// settings data as shell syntax.
func ValidRemoteExecutablePath(path string) bool {
	if len(path) == 0 || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	return (filepath.IsAbs(path) && path != "/") || (strings.HasPrefix(path, "~/") && len(path) > len("~/"))
}

// ExecutableForAgent returns the configured override for an agent, if any.
func (remote *RemoteSettings) ExecutableForAgent(agent string) string {
	if remote == nil || remote.Executables == nil {
		return ""
	}
	switch agent {
	case "claude":
		return remote.Executables.Claude
	case "codex":
		return remote.Executables.Codex
	case "opencode":
		return remote.Executables.OpenCode
	default:
		return ""
	}
}

// NewRemoteID returns a random path-safe remote source id.
func NewRemoteID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "r_" + hex.EncodeToString(raw), nil
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
	case BackendOpenCode:
		return "opencode"
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
	known := false
	for _, option := range BackendOptions() {
		if option.ID == config.Synthesis.Backend {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unsupported synthesis backend %q", config.Synthesis.Backend)
	}
	if !ValidSynthesisModel(config.Synthesis.Model) {
		return fmt.Errorf("model %q is not a valid model id", config.Synthesis.Model)
	}
	if config.Appearance.Theme != "light" && config.Appearance.Theme != "dark" {
		return fmt.Errorf("unsupported theme %q", config.Appearance.Theme)
	}
	if err := validateRemote(config.Remote); err != nil {
		return err
	}
	for _, option := range TerminalOptions() {
		if option.ID == config.Launch.Terminal {
			return nil
		}
	}
	return fmt.Errorf("unsupported terminal %q", config.Launch.Terminal)
}

func validateRemote(remote *RemoteSettings) error {
	if remote == nil {
		return nil
	}
	if !ValidRemoteID(remote.ID) {
		return fmt.Errorf("remote id %q is not a valid source id", remote.ID)
	}
	if !ValidSSHAlias(remote.SSHAlias) {
		return fmt.Errorf("remote sshAlias %q is not a valid SSH alias", remote.SSHAlias)
	}
	if remote.Executables == nil {
		return nil
	}
	paths := []struct {
		agent string
		path  string
	}{
		{agent: "claude", path: remote.Executables.Claude},
		{agent: "codex", path: remote.Executables.Codex},
		{agent: "opencode", path: remote.Executables.OpenCode},
	}
	nonblank := false
	for _, candidate := range paths {
		agent, path := candidate.agent, candidate.path
		if path != "" && !ValidRemoteExecutablePath(path) {
			return fmt.Errorf("remote executable for %s must be an absolute or ~/ path", agent)
		}
		nonblank = nonblank || path != ""
	}
	if !nonblank {
		return errors.New("remote executables must include at least one path")
	}
	return nil
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
	type appearanceDocument struct {
		Theme *string `json:"theme"`
	}
	type executablesDocument struct {
		Claude   *string `json:"claude"`
		Codex    *string `json:"codex"`
		OpenCode *string `json:"opencode"`
	}
	type remoteDocument struct {
		ID          *string              `json:"id"`
		SSHAlias    *string              `json:"sshAlias"`
		Enabled     *bool                `json:"enabled"`
		Executables *executablesDocument `json:"executables"`
	}
	type configDocument struct {
		Schema     *string             `json:"$schema"`
		Version    *int                `json:"version"`
		Synthesis  *synthesisDocument  `json:"synthesis"`
		Appearance *appearanceDocument `json:"appearance"`
		Launch     *launchDocument     `json:"launch"`
		Remote     *remoteDocument     `json:"remote"`
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
		Appearance: AppearanceSettings{Theme: "light"},
		Launch:     LaunchSettings{Terminal: *document.Launch.Terminal},
	}
	if document.Appearance != nil {
		if document.Appearance.Theme == nil {
			return Config{}, errors.New("appearance must include theme")
		}
		config.Appearance.Theme = *document.Appearance.Theme
	}
	if document.Remote != nil {
		if document.Remote.ID == nil || document.Remote.SSHAlias == nil || document.Remote.Enabled == nil {
			return Config{}, errors.New("remote must include id, sshAlias, and enabled")
		}
		config.Remote = &RemoteSettings{
			ID:       *document.Remote.ID,
			SSHAlias: *document.Remote.SSHAlias,
			Enabled:  *document.Remote.Enabled,
		}
		if document.Remote.Executables != nil {
			executables := RemoteExecutables{}
			paths := []struct {
				agent string
				value *string
			}{
				{agent: "claude", value: document.Remote.Executables.Claude},
				{agent: "codex", value: document.Remote.Executables.Codex},
				{agent: "opencode", value: document.Remote.Executables.OpenCode},
			}
			for _, candidate := range paths {
				agent, value := candidate.agent, candidate.value
				if value == nil {
					continue
				}
				if !ValidRemoteExecutablePath(*value) {
					return Config{}, fmt.Errorf("remote executable for %s must be an absolute or ~/ path", agent)
				}
				switch agent {
				case "claude":
					executables.Claude = *value
				case "codex":
					executables.Codex = *value
				case "opencode":
					executables.OpenCode = *value
				}
			}
			config.Remote.Executables = &executables
		}
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
