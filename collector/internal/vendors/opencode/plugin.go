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

const pluginName = "coslash-waiting.js"

//go:embed coslash-waiting.js
var waitingPlugin []byte

func EnsureWaitingPlugin() error {
	path, err := waitingPluginPath()
	if err != nil {
		return err
	}
	return installWaitingPlugin(filepath.Dir(path))
}

type WaitingPluginHealth struct {
	Path            string
	Installed       bool
	RestartRequired bool
	Err             error
}

func WaitingPluginDiagnostics() WaitingPluginHealth {
	path, err := waitingPluginPath()
	if err != nil {
		return WaitingPluginHealth{Err: err}
	}
	health := WaitingPluginHealth{Path: path}
	current, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return health
	}
	if err != nil {
		health.Err = err
		return health
	}
	if !bytes.Equal(current, waitingPlugin) {
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

func waitingPluginPath() (string, error) {
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

func installWaitingPlugin(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	path := filepath.Join(directory, pluginName)
	current, err := os.ReadFile(path)
	if err == nil && !strings.HasPrefix(string(current), "// managed by coSlash;") {
		return fmt.Errorf("refusing to overwrite unmanaged OpenCode plugin %s", path)
	}
	if err == nil && string(current) == string(waitingPlugin) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".coslash-waiting-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(waitingPlugin); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
