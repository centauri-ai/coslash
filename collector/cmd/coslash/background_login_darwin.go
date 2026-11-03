//go:build darwin

package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const backgroundLoginMarker = "COSLASH_LAUNCH_AGENT"

func backgroundLoginPlist(executable string) ([]byte, error) {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(executable)); err != nil {
		return nil, err
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>io.coslash.local</string>
<key>ProgramArguments</key><array><string>` + escaped.String() + `</string><string>--background</string></array>
<key>EnvironmentVariables</key><dict><key>` + backgroundLoginMarker + `</key><string>io.coslash.local</string></dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>15</integer>
</dict></plist>
`), nil
}

var backgroundLoginExecutable = os.Executable

func canInstallBackgroundLogin() bool {
	return releaseBuildMetadata == "true" && branchBuildMetadata != "true" && usesDefaultHome()
}

func registerBackgroundLogin() error {
	if !canInstallBackgroundLogin() {
		return nil
	}
	return installBackgroundLogin()
}

func installService() error {
	if !canInstallBackgroundLogin() {
		return fmt.Errorf("login installation requires a release build and the default COSLASH_HOME")
	}
	if err := installBackgroundLogin(); err != nil {
		return err
	}
	return registerProtocolHandler()
}

func installBackgroundLogin() error {
	executable, err := backgroundLoginExecutable()
	if err != nil {
		return err
	}
	stable := backgroundLoginInstallPath(executable)
	if !sameProtocolHandlerPath(stable, executable) {
		return fmt.Errorf("login executable does not resolve to this installation")
	}
	executable = stable
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
	replaced := false
	if _, err := os.Lstat(path); err == nil {
		replaced = true
		output, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", "--", path).Output()
		if err != nil {
			return fmt.Errorf("read existing login item: %w", err)
		}
		var existing struct {
			Label                string
			Program              string
			ProgramArguments     []string
			EnvironmentVariables map[string]string
		}
		if err := json.Unmarshal(output, &existing); err != nil {
			return err
		}
		previous, key := existing.Program, "Program"
		if previous == "" && len(existing.ProgramArguments) > 0 {
			previous, key = existing.ProgramArguments[0], "ProgramArguments.0"
		}
		env := existing.EnvironmentVariables
		if existing.Label != "io.coslash.local" || !filepath.IsAbs(previous) || backgroundLoginInstallPath(previous) != executable ||
			(env["HOME"] != "" && (!filepath.IsAbs(env["HOME"]) || !sameProtocolHandlerPath(env["HOME"], home))) ||
			(env["COSLASH_HOME"] != "" && (!filepath.IsAbs(env["COSLASH_HOME"]) || !sameProtocolHandlerPath(env["COSLASH_HOME"], filepath.Join(home, ".coslash")))) {
			return fmt.Errorf("existing login item belongs to another installation or home")
		}
		// Replace the whole array: plutil -replace ProgramArguments.0 inserts
		// a new first item and keeps the old one, which breaks the job.
		value, valueType := executable, "-string"
		if key == "ProgramArguments.0" {
			arguments := repairBackgroundLoginArguments(existing.ProgramArguments, executable)
			if slices.Equal(existing.ProgramArguments, arguments) {
				return loadBackgroundLogin(path)
			}
			encoded, err := json.Marshal(arguments)
			if err != nil {
				return err
			}
			key, value, valueType = "ProgramArguments", string(encoded), "-json"
		} else if previous == executable {
			return loadBackgroundLogin(path)
		}
		contents, err = exec.Command("/usr/bin/plutil", "-replace", key, valueType, value, "-o", "-", "--", path).Output()
		if err != nil {
			return fmt.Errorf("update login executable: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
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
	if replaced {
		return reloadBackgroundLogin(path)
	}
	return loadBackgroundLogin(path)
}

func repairBackgroundLoginArguments(existing []string, executable string) []string {
	arguments := make([]string, 0, len(existing)+1)
	arguments = append(arguments, executable)
	for _, argument := range existing[1:] {
		if filepath.IsAbs(argument) && backgroundLoginInstallPath(argument) == executable {
			continue
		}
		arguments = append(arguments, argument)
	}
	if !slices.Contains(arguments[1:], "--background") {
		arguments = slices.Insert(arguments, 1, "--background")
	}
	return arguments
}

// launchd keeps the definition it loaded, so a replaced file needs a reload.
// A process belonging to the job must leave it loaded because bootout stops it.
func reloadBackgroundLogin(path string) error {
	loaded, err := exec.Command("launchctl", "print", backgroundLoginTarget()).Output()
	if err == nil {
		current, err := backgroundLoginIsCurrentProcess(loaded)
		if err != nil {
			return err
		}
		if !current {
			if output, err := exec.Command("launchctl", "bootout", backgroundLoginTarget()).CombinedOutput(); err != nil && backgroundLoginLoaded() {
				return fmt.Errorf("launchctl bootout: %w: %s", err, strings.TrimSpace(string(output)))
			}
		}
	}
	return loadBackgroundLogin(path)
}

var backgroundLoginPID = regexp.MustCompile(`(?m)^\s*pid = ([0-9]+)\s*$`)

func backgroundLoginIsCurrentProcess(output []byte) (bool, error) {
	if os.Getenv(backgroundLoginMarker) == "io.coslash.local" {
		return true, nil
	}
	match := backgroundLoginPID.FindSubmatch(output)
	if len(match) != 2 {
		return false, fmt.Errorf("cannot safely reload login item: launchctl did not report its process id")
	}
	pid, err := strconv.Atoi(string(match[1]))
	if err != nil {
		return false, fmt.Errorf("read login item process id: %w", err)
	}
	return pid == os.Getpid(), nil
}

// Homebrew's opt link survives Cellar upgrades, including removal of the old keg.
func backgroundLoginInstallPath(executable string) string {
	resolved := canonicalPath(executable)
	bin := filepath.Dir(resolved)
	keg := filepath.Dir(bin)
	formula := filepath.Dir(keg)
	cellar := filepath.Dir(formula)
	if filepath.Base(resolved) == "coslash" && filepath.Base(bin) == "bin" && filepath.Base(formula) == "coslash" && filepath.Base(cellar) == "Cellar" {
		return filepath.Join(canonicalPath(filepath.Dir(cellar)), "opt", "coslash", "bin", "coslash")
	}
	return resolved
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
