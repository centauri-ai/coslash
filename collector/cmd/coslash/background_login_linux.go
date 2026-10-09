//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	systemdUnitName = "coslash.service"
	cronMarker      = "# coSlash Local"
)

var backgroundLookPath = exec.LookPath

var backgroundCommand = func(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// startSupervisedBackground starts Local under the systemd user manager when
// one is reachable and lingers, so Restart= and KillMode= apply from the first
// launch. Without lingering the user manager stops at logout and would take
// Local with it, so the caller starts a detached process instead. The unit is
// enabled for boot only after pairing, in registerBackgroundLogin.
func startSupervisedBackground() bool {
	if !systemdUserAvailable() || !ensureLinger() {
		return false
	}
	if err := writeSystemdUnit(); err != nil {
		return false
	}
	_, err := backgroundCommand("systemctl", "--user", "start", systemdUnitName)
	return err == nil
}

func registerBackgroundLogin() error {
	_, err := ensureBackgroundPersistence()
	return err
}

// ensureBackgroundPersistence makes Local start at boot and reports how. A
// systemd user unit needs lingering to start without a login and to outlive
// the SSH session; without it (stock Ubuntu refuses enable-linger to a remote
// session without sudo) Local falls back to a crontab @reboot entry.
func ensureBackgroundPersistence() (string, error) {
	if systemdUserAvailable() && ensureLinger() {
		if err := writeSystemdUnit(); err != nil {
			return "", err
		}
		if output, err := backgroundCommand("systemctl", "--user", "enable", systemdUnitName); err != nil {
			return "", fmt.Errorf("systemctl --user enable: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return "systemd", nil
	}
	if _, err := backgroundLookPath("crontab"); err == nil {
		if err := installCronEntry(); err != nil {
			return "", err
		}
		return "cron", nil
	}
	return "none", errors.New("no systemd user manager or crontab is available")
}

// reportBackgroundPersistence runs from the interactive connect command so
// that enabling lingering can use the caller's login session.
func reportBackgroundPersistence(stdout io.Writer) {
	mode, err := ensureBackgroundPersistence()
	if err != nil {
		log.Printf("register background coSlash Local: %v", err)
	}
	switch mode {
	case "systemd":
		fmt.Fprintln(stdout, "✓ coSlash Local runs as a systemd user service (coslash.service) and starts at boot.")
	case "cron":
		fmt.Fprintln(stdout, "✓ coSlash Local starts at boot from your crontab (@reboot).")
	default:
		fmt.Fprintln(stdout, "coSlash Local is running now but will not start after a reboot: no systemd user manager or crontab was found.")
		fmt.Fprintln(stdout, "Start it from your init system or container entrypoint with: coslash --background")
	}
}

func backgroundLoginLoaded() bool {
	if !systemdUserAvailable() {
		return false
	}
	output, err := backgroundCommand("systemctl", "--user", "show", systemdUnitName, "--property=ActiveState", "--value")
	if err != nil {
		return false
	}
	switch strings.TrimSpace(string(output)) {
	case "active", "activating", "deactivating", "reloading":
		return true
	}
	return false
}

func systemdUserAvailable() bool {
	if _, err := backgroundLookPath("systemctl"); err != nil {
		return false
	}
	output, err := backgroundCommand("systemctl", "--user", "is-system-running")
	state := strings.TrimSpace(string(output))
	return err == nil || state == "degraded" || state == "starting"
}

func ensureLinger() bool {
	if lingerEnabled() {
		return true
	}
	_, err := backgroundCommand("loginctl", "enable-linger")
	return err == nil && lingerEnabled()
}

func lingerEnabled() bool {
	output, err := backgroundCommand("loginctl", "show-user", fmt.Sprint(os.Getuid()), "--property=Linger", "--value")
	return err == nil && strings.TrimSpace(string(output)) == "yes"
}

func writeSystemdUnit() error {
	executable, err := backgroundExecutable()
	if err != nil {
		return err
	}
	contents, err := systemdUnit(executable, os.Getenv)
	if err != nil {
		return err
	}
	dir, err := systemdUserUnitDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, systemdUnitName)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, contents) {
		return nil
	}
	if err := writePrivateFile(dir, path, contents); err != nil {
		return err
	}
	if output, err := backgroundCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func systemdUserUnitDir() (string, error) {
	if config := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(config) {
		return filepath.Join(config, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// The systemd user manager does not read shell profiles, so the unit carries
// the variables that change where Local or the agents keep their data.
var systemdUnitEnvironment = []string{
	"COSLASH_HOME", "PATH", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
	"GROK_HOME", "OPENCODE_DB", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "COSLASH_PI_SESSION_ROOTS",
}

func systemdUnit(executable string, getenv func(string) string) ([]byte, error) {
	quotedExecutable, err := systemdQuote(executable)
	if err != nil {
		return nil, err
	}
	var unit strings.Builder
	unit.WriteString("[Unit]\nDescription=coSlash Local\n\n[Service]\n")
	unit.WriteString("ExecStart=" + quotedExecutable + " --background\n")
	for _, name := range systemdUnitEnvironment {
		value := getenv(name)
		if value == "" {
			continue
		}
		quoted, err := systemdQuote(name + "=" + value)
		if err != nil {
			continue
		}
		unit.WriteString("Environment=" + quoted + "\n")
	}
	// KillMode=process keeps the automatic-update helper alive while the main
	// process exits; the short restart delay stays inside its 30s readiness wait.
	unit.WriteString("Restart=always\nRestartSec=5\nKillMode=process\n\n[Install]\nWantedBy=default.target\n")
	return []byte(unit.String()), nil
}

func systemdQuote(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\n\r") {
		return "", errors.New("value cannot be written to a systemd unit")
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + replacer.Replace(value) + `"`, nil
}

func installCronEntry() error {
	executable, err := backgroundExecutable()
	if err != nil {
		return err
	}
	current, err := backgroundCommand("crontab", "-l")
	if err != nil && !strings.Contains(strings.ToLower(string(current)), "no crontab") {
		return fmt.Errorf("crontab -l: %w", err)
	}
	if err != nil {
		current = nil
	}
	updated, changed, err := crontabWithEntry(string(current), executable, os.Getenv("COSLASH_HOME"))
	if err != nil || !changed {
		return err
	}
	return writeCrontab(updated)
}

var writeCrontab = func(contents string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "crontab", "-")
	command.Stdin = strings.NewReader(contents)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab -: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func crontabWithEntry(current, executable, coslashHome string) (string, bool, error) {
	entry := "@reboot " + shellQuote(executable) + " --background >/dev/null 2>&1 " + cronMarker
	if coslashHome != "" {
		entry = "@reboot COSLASH_HOME=" + shellQuote(coslashHome) + " " + strings.TrimPrefix(entry, "@reboot ")
	}
	if strings.ContainsAny(executable+coslashHome, "\n\r%") {
		return "", false, errors.New("path cannot be written to a crontab")
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(current, "\n"), "\n") {
		if line == entry {
			return current, false, nil
		}
		if line != "" && !strings.HasSuffix(line, cronMarker) {
			lines = append(lines, line)
		}
	}
	lines = append(lines, entry)
	return strings.Join(lines, "\n") + "\n", true, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func backgroundExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(executable)
}

func writePrivateFile(dir, path string, contents []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".coslash-*")
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
	return os.Rename(temporary.Name(), path)
}
