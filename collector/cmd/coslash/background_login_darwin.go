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
	"strings"
)

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
	if _, err := os.Lstat(path); err == nil {
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
		if previous == executable {
			return loadBackgroundLogin(path)
		}
		contents, err = exec.Command("/usr/bin/plutil", "-replace", key, "-string", executable, "-o", "-", "--", path).Output()
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
	return loadBackgroundLogin(path)
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
