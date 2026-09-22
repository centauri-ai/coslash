package opencode

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pluginName       = "coslash-plugin.js"
	legacyPluginName = "coslash-waiting.js"
)

//go:embed coslash-plugin.js
var pluginSourceV1 []byte

const pluginV2Suffix = `
export default { id: "coslash", setup: setupV2 }
`

var (
	detectOpenCodeVersion = openCodeVersion
	pluginInstallation    sync.Mutex
)

func EnsurePlugin() error {
	pluginInstallation.Lock()
	defer pluginInstallation.Unlock()
	path, err := pluginPath()
	if err != nil {
		return err
	}
	return installPlugin(filepath.Dir(path))
}

type PluginHealth struct {
	Path            string
	Installed       bool
	RestartRequired bool
	Err             error
}

func PluginDiagnostics() PluginHealth {
	path, err := pluginPath()
	if err != nil {
		return PluginHealth{Err: err}
	}
	health := PluginHealth{Path: path}
	current, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return health
	}
	if err != nil {
		health.Err = err
		return health
	}
	wanted, err := managedPluginSource()
	if err != nil {
		health.Err = err
		return health
	}
	if !bytes.Equal(current, wanted) {
		health.Err = errors.New("installed plugin differs from the coSlash plugin")
		return health
	}
	health.Installed = true
	info, err := os.Stat(path)
	if err != nil {
		health.Err = err
		return health
	}
	processes, err := listTUIProcesses()
	if err != nil {
		health.Err = fmt.Errorf("list OpenCode processes: %w", err)
		return health
	}
	for _, process := range processes {
		if processWorkingDirectory(process.pid) != "" && process.startedAt < info.ModTime().UnixMilli() {
			health.RestartRequired = true
			break
		}
	}
	return health
}

func pluginPath() (string, error) {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, "opencode", "plugins", pluginName), nil
}

func installPlugin(directory string) error {
	source, err := managedPluginSource()
	if err != nil {
		return err
	}
	return installPluginSource(directory, source)
}

func installPluginSource(directory string, source []byte) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, pluginName)
	current, err := os.ReadFile(path)
	if err == nil && !strings.HasPrefix(string(current), "// managed by coSlash;") {
		return fmt.Errorf("refusing to overwrite unmanaged OpenCode plugin %s", path)
	}
	if err == nil && bytes.Equal(current, source) {
		return removeManagedLegacyPlugin(directory)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".coslash-plugin-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(source); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return err
	}
	return removeManagedLegacyPlugin(directory)
}

func managedPluginSource() ([]byte, error) {
	version, err := detectOpenCodeVersion()
	if err != nil {
		return nil, fmt.Errorf("detect OpenCode version: %w", err)
	}
	if openCodeMajor(version) == 0 {
		return nil, fmt.Errorf("detect OpenCode version: unrecognized output %q", version)
	}
	return pluginSourceForVersion(version), nil
}

func pluginSourceForVersion(version string) []byte {
	if openCodeMajor(version) < 2 {
		return pluginSourceV1
	}
	source := make([]byte, 0, len(pluginSourceV1)+len(pluginV2Suffix))
	source = append(source, pluginSourceV1...)
	return append(source, pluginV2Suffix...)
}

func openCodeMajor(version string) int {
	for _, field := range strings.Fields(version) {
		field = strings.TrimPrefix(field, "v")
		major, _, found := strings.Cut(field, ".")
		if !found {
			continue
		}
		value, err := strconv.Atoi(major)
		if err == nil {
			return value
		}
	}
	return 0
}

func openCodeVersion() (string, error) {
	executable, err := exec.LookPath("opencode")
	if err != nil {
		return "", nil
	}
	// OpenCode v2 opens its shared log even for --version. On Windows that file
	// may be locked by a running TUI, so isolate the probe's data directory.
	probeData, err := os.MkdirTemp("", "coslash-opencode-version-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(probeData)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "--version")
	command.Env = append(os.Environ(), "XDG_DATA_HOME="+probeData)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0]), nil
}

func removeManagedLegacyPlugin(directory string) error {
	path := filepath.Join(directory, legacyPluginName)
	current, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(current), "// managed by coSlash;") {
		return nil
	}
	return os.Remove(path)
}
