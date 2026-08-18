package opencode

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const pluginName = "coslash-waiting.js"

//go:embed coslash-waiting.js
var waitingPlugin []byte

func EnsureWaitingPlugin() error {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		root = filepath.Join(home, ".config")
	}
	return installWaitingPlugin(filepath.Join(root, "opencode", "plugins"))
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
