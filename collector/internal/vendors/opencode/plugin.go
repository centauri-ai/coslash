package opencode

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	pluginName       = "coslash-plugin.js"
	legacyPluginName = "coslash-waiting.js"
)

//go:embed coslash-plugin.js
var pluginSource []byte

func EnsurePlugin() error {
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
	if !bytes.Equal(current, pluginSource) {
		health.Err = errors.New("installed plugin differs from the coSlash plugin")
		return health
	}
	health.Installed = true
	info, err := os.Stat(path)
	if err != nil {
		health.Err = err
		return health
	}
	output, err := exec.Command("ps", "-ww", "-axo", "pid=,lstart=,command=").Output()
	if err != nil {
		health.Err = fmt.Errorf("list OpenCode processes: %w", err)
		return health
	}
	for _, process := range parseTUIProcesses(string(output)) {
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
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, pluginName)
	current, err := os.ReadFile(path)
	if err == nil && !strings.HasPrefix(string(current), "// managed by coSlash;") {
		return fmt.Errorf("refusing to overwrite unmanaged OpenCode plugin %s", path)
	}
	if err == nil && string(current) == string(pluginSource) {
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
	if _, err := temporary.Write(pluginSource); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return removeManagedLegacyPlugin(directory)
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
