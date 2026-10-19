//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const linuxProtocolDesktopFile = "io.coslash.local.desktop"

type linuxProtocolHandlerBackend struct{}

func newProtocolHandlerBackend() protocolHandlerBackend { return linuxProtocolHandlerBackend{} }

func (linuxProtocolHandlerBackend) Register(executable, home string, previous protocolHandlerRecord) (string, error) {
	dataHome := xdgDataHome(home)
	applications := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(applications, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(applications, linuxProtocolDesktopFile)
	contents := linuxProtocolDesktopEntry(executable)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return "", err
	}
	command, err := exec.LookPath("xdg-mime")
	if err != nil {
		return "", fmt.Errorf("find xdg-mime to register coslash: links: %w", err)
	}
	if output, err := exec.Command(command, "default", linuxProtocolDesktopFile, "x-scheme-handler/coslash").CombinedOutput(); err != nil {
		return "", fmt.Errorf("register coslash: URL handler: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	if err := unregisterPreviousProtocolHandler(previous.Path, path, func(previousPath string) error {
		if filepath.Base(previousPath) != linuxProtocolDesktopFile {
			return fmt.Errorf("refusing to remove unexpected previous protocol handler path %q", previousPath)
		}
		if err := os.Remove(previousPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}); err != nil {
		return "", err
	}
	return path, nil
}

func (linuxProtocolHandlerBackend) Status(executable, home string) (protocolHandlerRegistration, error) {
	registeredDesktop, err := defaultLinuxProtocolDesktop(home)
	if err != nil || registeredDesktop != linuxProtocolDesktopFile {
		return protocolHandlerRegistration{}, err
	}
	path := filepath.Join(xdgDataHome(home), "applications", linuxProtocolDesktopFile)
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return protocolHandlerRegistration{}, nil
	}
	if err != nil {
		return protocolHandlerRegistration{}, err
	}
	if !linuxDesktopClaimsCoslash(string(contents)) {
		return protocolHandlerRegistration{}, nil
	}
	target := linuxProtocolExecutable(string(contents))
	if target == "" {
		return protocolHandlerRegistration{}, nil
	}
	return protocolHandlerRegistration{Registered: true, Executable: target, Path: path}, nil
}

func (linuxProtocolHandlerBackend) StaleHandlers(_, _ string) ([]string, error) {
	return nil, fmt.Errorf("coslash doctor --fix-links is only available on macOS")
}

func (linuxProtocolHandlerBackend) Unregister(path string) error {
	if filepath.Base(path) != linuxProtocolDesktopFile {
		return fmt.Errorf("refusing to remove unexpected protocol handler path %q", path)
	}
	return os.Remove(path)
}

func xdgDataHome(home string) string {
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		return value
	}
	return filepath.Join(home, ".local", "share")
}

func xdgConfigHome(home string) string {
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		return value
	}
	return filepath.Join(home, ".config")
}

func linuxProtocolDesktopEntry(executable string) string {
	return "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=coSlash Local\n" +
		"NoDisplay=true\n" +
		"Exec=" + quoteDesktopExecArgument(executable) + " --protocol-url %u\n" +
		"MimeType=x-scheme-handler/coslash;\n"
}

func quoteDesktopExecArgument(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func defaultLinuxProtocolDesktop(home string) (string, error) {
	paths := []string{
		filepath.Join(xdgConfigHome(home), "mimeapps.list"),
		filepath.Join(xdgDataHome(home), "applications", "mimeapps.list"),
		filepath.Join(home, ".local", "share", "applications", "mimeapps.list"),
		filepath.Join(home, ".config", "mimeapps.list"),
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if desktop := linuxProtocolDefaultInMimeApps(string(contents)); desktop != "" {
			return desktop, nil
		}
	}
	return "", nil
}

func linuxProtocolDefaultInMimeApps(contents string) string {
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
			continue
		}
		if section != "[Default Applications]" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "x-scheme-handler/coslash" {
			return strings.TrimSpace(strings.Split(value, ";")[0])
		}
	}
	return ""
}

func linuxProtocolExecutable(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Exec=") {
			continue
		}
		value := strings.TrimPrefix(line, "Exec=")
		if strings.HasPrefix(value, `"`) {
			var executable strings.Builder
			escaped := false
			for _, char := range value[1:] {
				if escaped {
					executable.WriteRune(char)
					escaped = false
					continue
				}
				if char == '\\' {
					escaped = true
					continue
				}
				if char == '"' {
					return strings.ReplaceAll(executable.String(), "%%", "%")
				}
				executable.WriteRune(char)
			}
			return ""
		}
		fields := strings.Fields(value)
		if len(fields) > 0 {
			return strings.ReplaceAll(fields[0], "%%", "%")
		}
	}
	return ""
}

func linuxDesktopClaimsCoslash(contents string) bool {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "MimeType=") {
			continue
		}
		for _, mime := range strings.Split(strings.TrimPrefix(line, "MimeType="), ";") {
			if mime == "x-scheme-handler/coslash" {
				return true
			}
		}
	}
	return false
}
