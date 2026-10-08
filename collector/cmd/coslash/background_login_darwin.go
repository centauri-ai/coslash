//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func registerBackgroundLogin() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "io.coslash.local.plist")
	contents, err := backgroundLoginPlist(executable)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, contents) {
		return loadBackgroundLogin(path)
	}
	temporary, err := os.CreateTemp(dir, ".coslash-local-*.plist")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	return loadBackgroundLogin(path)
}

func backgroundLoginTarget() string {
	return fmt.Sprintf("gui/%d/io.coslash.local", os.Getuid())
}

func backgroundLoginLoaded() bool {
	return exec.Command("launchctl", "print", backgroundLoginTarget()).Run() == nil
}

func loadBackgroundLogin(path string) error {
	if backgroundLoginLoaded() {
		return nil
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	command := exec.Command("launchctl", "bootstrap", domain, path)
	output, err := command.CombinedOutput()
	if err != nil && !backgroundLoginLoaded() {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
