//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	_, err := backgroundCommand("systemctl", "--user", "start", backgroundUnitName())
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
		if output, err := backgroundCommand("systemctl", "--user", "enable", backgroundUnitName()); err != nil {
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
		fmt.Fprintf(stdout, "✓ coSlash Local runs as a systemd user service (%s) and starts at boot.\n", backgroundUnitName())
	case "cron":
		fmt.Fprintln(stdout, "✓ coSlash Local starts at boot from your crontab (@reboot).")
	default:
		fmt.Fprintln(stdout, "coSlash Local is running now but automatic startup could not be configured.")
		fmt.Fprintln(stdout, "Start it from your init system or container entrypoint with: coslash --background")
	}
}

func backgroundLoginLoaded() bool {
	executable, _ := backgroundExecutable()
	return backgroundLoginLoadedForTarget(executable)
}

func backgroundLoginLoadedForTarget(executable string) bool {
	if !systemdUserAvailable() {
		return false
	}
	output, err := backgroundCommand("systemctl", "--user", "show", backgroundUnitNameFor(executable), "--property=ActiveState", "--value")
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
	path := filepath.Join(dir, backgroundUnitName())
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("existing coSlash user service is not a regular file")
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Equal(existing, contents) {
			return nil
		}
		if !ownedSystemdUnit(existing, executable, os.Getenv("COSLASH_HOME")) {
			return errors.New("existing coSlash user service belongs to another installation")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
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
	output, err := backgroundCommand("systemctl", "--user", "show-environment")
	if err != nil {
		return "", fmt.Errorf("read systemd user environment: %w", err)
	}
	managerHome := ""
	for _, line := range strings.Split(string(output), "\n") {
		if config, ok := strings.CutPrefix(line, "XDG_CONFIG_HOME="); ok && filepath.IsAbs(config) {
			return filepath.Join(config, "systemd", "user"), nil
		}
		if home, ok := strings.CutPrefix(line, "HOME="); ok && filepath.IsAbs(home) {
			managerHome = home
		}
	}
	if managerHome != "" {
		return filepath.Join(managerHome, ".config", "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func backgroundProfileSuffix(home string) string {
	if home == "" {
		return ""
	}
	if absolute, err := filepath.Abs(home); err == nil {
		home = absolute
	}
	if userHome, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == filepath.Join(userHome, ".coslash") {
		return ""
	}
	sum := sha256.Sum256([]byte(filepath.Clean(home)))
	return fmt.Sprintf("%x", sum[:8])
}

func backgroundUnitName() string {
	executable, _ := backgroundExecutable()
	return backgroundUnitNameFor(executable)
}

func backgroundUnitNameFor(executable string) string {
	if suffix := backgroundProfileSuffix(os.Getenv("COSLASH_HOME")); suffix != "" {
		// Keep an older profile-specific installation on its existing service.
		// Renaming a running service would race its automatic restart with the
		// update helper and could launch a second copy.
		if executable != "" {
			if dir, err := systemdUserUnitDir(); err == nil {
				legacy := filepath.Join(dir, systemdUnitName)
				if info, err := os.Lstat(legacy); err == nil && info.Mode().IsRegular() {
					if contents, err := os.ReadFile(legacy); err == nil && ownedSystemdUnit(contents, executable, os.Getenv("COSLASH_HOME")) {
						return systemdUnitName
					}
				}
			}
		}
		return "coslash-" + suffix + ".service"
	}
	return systemdUnitName
}

func ownedSystemdUnit(existing []byte, executable, coslashHome string) bool {
	quoted, err := systemdQuote(executable)
	if err != nil {
		return false
	}
	wantExec := "ExecStart=" + quoted + " --background"
	wantHome := ""
	if coslashHome != "" {
		quoted, err := systemdQuote("COSLASH_HOME=" + coslashHome)
		if err != nil {
			return false
		}
		wantHome = "Environment=" + quoted
	}
	seenExec, seenHome := false, false
	allowed := make(map[string]bool, len(systemdUnitEnvironment))
	for _, name := range systemdUnitEnvironment {
		allowed[name] = true
	}
	for _, line := range strings.Split(strings.TrimSpace(string(existing)), "\n") {
		switch line {
		case "", "[Unit]", "Description=coSlash Local", "[Service]", "Restart=always", "RestartSec=5", "KillMode=process", "[Install]", "WantedBy=default.target":
			continue
		case wantExec:
			if seenExec {
				return false
			}
			seenExec = true
		default:
			if !strings.HasPrefix(line, `Environment="`) || !strings.HasSuffix(line, `"`) {
				return false
			}
			name, _, ok := strings.Cut(strings.TrimPrefix(line, `Environment="`), "=")
			if !ok || !allowed[name] {
				return false
			}
			delete(allowed, name)
			if name == "COSLASH_HOME" {
				if line != wantHome {
					return false
				}
				seenHome = true
			}
		}
	}
	return seenExec && (wantHome == "" && !seenHome || wantHome != "" && seenHome)
}

// The systemd user manager does not read shell profiles, so the unit carries
// the variables that change where Local or the agents keep their data.
var systemdUnitEnvironment = []string{
	"COSLASH_HOME", "PATH", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "COSLASH_INSTALL_DIR",
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
	marker := cronMarker
	if suffix := backgroundProfileSuffix(coslashHome); suffix != "" {
		marker += " " + suffix
	}
	entry := "@reboot " + shellQuote(executable) + " --background >/dev/null 2>&1 " + marker
	if coslashHome != "" {
		entry = "@reboot COSLASH_HOME=" + shellQuote(coslashHome) + " " + strings.TrimPrefix(entry, "@reboot ")
	}
	if strings.ContainsAny(executable+coslashHome, "\n\r%") {
		return "", false, errors.New("path cannot be written to a crontab")
	}
	var lines []string
	found, changed := false, false
	for _, line := range strings.Split(strings.TrimRight(current, "\n"), "\n") {
		if line == entry {
			if found {
				changed = true
			} else {
				lines = append(lines, line)
				found = true
			}
			continue
		}
		matchesMarker := strings.HasSuffix(line, marker)
		if marker == cronMarker && strings.HasPrefix(line, "@reboot COSLASH_HOME=") && (coslashHome == "" || !strings.HasPrefix(line, "@reboot COSLASH_HOME="+shellQuote(coslashHome)+" ")) {
			matchesMarker = false
		}
		if matchesMarker || marker != cronMarker && legacyCronEntryForHome(line, coslashHome) {
			changed = true
		} else if line != "" {
			lines = append(lines, line)
		}
	}
	if !found {
		lines = append(lines, entry)
		changed = true
	}
	if !changed {
		return current, false, nil
	}
	return strings.Join(lines, "\n") + "\n", true, nil
}

func legacyCronEntryForHome(line, home string) bool {
	if home == "" {
		return false
	}
	prefix := "@reboot COSLASH_HOME=" + shellQuote(home) + " "
	suffix := " --background >/dev/null 2>&1 " + cronMarker
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return false
	}
	quoted := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
	if len(quoted) < 2 || quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' {
		return false
	}
	path := strings.ReplaceAll(quoted[1:len(quoted)-1], `'\''`, "'")
	return filepath.IsAbs(path) && filepath.Base(path) == "coslash" && shellQuote(path) == quoted
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
