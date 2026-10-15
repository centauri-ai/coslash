//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	macProtocolBundleName = "coSlash Local Pairing Handler.app"
	macProtocolBundleID   = "io.coslash.local.pairing-handler"
	launchServicesTool    = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
)

type darwinProtocolHandlerBackend struct{}

func newProtocolHandlerBackend() protocolHandlerBackend { return darwinProtocolHandlerBackend{} }

func (darwinProtocolHandlerBackend) Register(executable, home string, previous protocolHandlerRecord) (string, error) {
	parent := filepath.Join(home, "Library", "Application Support", "coSlash Local")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	bundle := filepath.Join(parent, macProtocolBundleName)
	marker := filepath.Join(bundle, "Contents", "Resources", "coslash-executable")
	contents, readErr := os.ReadFile(marker)
	if readErr != nil || string(contents) != executable {
		if err := writeMacProtocolBundle(parent, bundle, executable); err != nil {
			return "", err
		}
	}
	if err := exec.Command("/usr/bin/open", "-a", bundle).Run(); err != nil {
		return "", err
	}
	if err := unregisterPreviousProtocolHandler(previous.Path, bundle, func(path string) error {
		if filepath.Base(path) != macProtocolBundleName {
			return fmt.Errorf("refusing to unregister unexpected previous protocol handler path %q", path)
		}
		return unregisterMacProtocolBundle(path)
	}); err != nil {
		return "", fmt.Errorf("unregister previous protocol handler: %w", err)
	}
	return bundle, nil
}

func writeMacProtocolBundle(parent, bundle, executable string) error {
	work, err := os.MkdirTemp(parent, ".coslash-handler-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	app := filepath.Join(work, macProtocolBundleName)
	scriptPath := filepath.Join(work, "handler.applescript")
	script := "on run\nend run\n\non open location this_url\n" +
		"do shell script \"/usr/bin/nohup \" & quoted form of " + appleScriptLiteral(executable) +
		" & \" --protocol-url \" & quoted form of this_url & \" >/dev/null 2>&1 &\"\nend open location\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return err
	}
	if output, err := exec.Command("/usr/bin/osacompile", "-o", app, scriptPath).CombinedOutput(); err != nil {
		return fmt.Errorf("compile protocol handler: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	info := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleDevelopmentRegion</key><string>English</string>
<key>CFBundleExecutable</key><string>applet</string>
<key>CFBundleIdentifier</key><string>` + macProtocolBundleID + `</string>
<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
<key>CFBundleName</key><string>coSlash Local Pairing Handler</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>1.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSUIElement</key><true/>
<key>CFBundleURLTypes</key><array><dict>
<key>CFBundleURLName</key><string>coSlash Local pairing</string>
<key>CFBundleURLSchemes</key><array><string>coslash</string></array>
</dict></array>
</dict></plist>
`
	contents := filepath.Join(app, "Contents")
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(info), 0o600); err != nil {
		return err
	}
	resources := filepath.Join(contents, "Resources")
	if err := os.WriteFile(filepath.Join(resources, "coslash-executable"), []byte(executable), 0o600); err != nil {
		return err
	}
	backup := ""
	if _, err := os.Stat(bundle); err == nil {
		backup, err = os.MkdirTemp(parent, ".coslash-handler-backup-")
		if err != nil {
			return err
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
		if err := os.Rename(bundle, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(app, bundle); err != nil {
		if backup != "" {
			_ = os.Rename(backup, bundle)
		}
		return err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func (darwinProtocolHandlerBackend) Status(executable, home string) (protocolHandlerRegistration, error) {
	handlers, err := registeredMacProtocolHandlers(home)
	if err != nil {
		return protocolHandlerRegistration{}, err
	}
	return macProtocolHandlerStatus(executable, home, handlers), nil
}

func macProtocolHandlerStatus(executable, home string, handlers []launchServicesHandler) protocolHandlerRegistration {
	current := filepath.Join(home, "Library", "Application Support", "coSlash Local", macProtocolBundleName)
	var foundCurrent *protocolHandlerRegistration
	for _, handler := range handlers {
		if filepath.Base(handler.Path) != macProtocolBundleName {
			continue
		}
		registeredExecutable := readMacProtocolExecutable(handler.Path)
		registration := protocolHandlerRegistration{Registered: true, Executable: registeredExecutable, Path: handler.Path}
		if sameProtocolHandlerPath(handler.Path, current) && sameExecutablePath(registeredExecutable, executable) {
			foundCurrent = &registration
			continue
		}
		if sameProtocolHandlerPath(handler.Path, current) && registeredExecutable != "" && canInstallBackgroundLogin() {
			if _, err := os.Stat(registeredExecutable); os.IsNotExist(err) {
				continue
			}
		}
		if registration.Executable == "" {
			registration.Executable = handler.Path
		}
		return registration
	}
	if foundCurrent != nil {
		return *foundCurrent
	}
	return protocolHandlerRegistration{}
}

func (darwinProtocolHandlerBackend) StaleHandlers(_ string, home string) ([]string, error) {
	handlers, err := registeredMacProtocolHandlers(home)
	if err != nil {
		return nil, err
	}
	current := filepath.Join(home, "Library", "Application Support", "coSlash Local", macProtocolBundleName)
	seen := make(map[string]struct{})
	var stale []string
	for _, handler := range handlers {
		if filepath.Base(handler.Path) != macProtocolBundleName {
			continue
		}
		_, statErr := os.Stat(handler.Path)
		if sameProtocolHandlerPath(handler.Path, current) && !os.IsNotExist(statErr) {
			continue
		}
		if _, exists := seen[handler.Path]; exists {
			continue
		}
		seen[handler.Path] = struct{}{}
		stale = append(stale, handler.Path)
	}
	return stale, nil
}

func (darwinProtocolHandlerBackend) Unregister(path string) error {
	if filepath.Base(path) != macProtocolBundleName {
		return fmt.Errorf("refusing to unregister unexpected protocol handler path %q", path)
	}
	return unregisterMacProtocolBundle(path)
}

type launchServicesHandler struct {
	Path          string
	ClaimsCoslash bool
}

func registeredMacProtocolHandlers(home string) ([]launchServicesHandler, error) {
	output, err := exec.Command(launchServicesTool, "-dump").Output()
	if err != nil {
		return nil, fmt.Errorf("read LaunchServices registrations: %w", err)
	}
	parsed := parseLaunchServicesDump(string(output))
	for index := range parsed {
		if strings.HasPrefix(parsed[index].Path, "~/") {
			parsed[index].Path = filepath.Join(home, strings.TrimPrefix(parsed[index].Path, "~/"))
		}
		parsed[index].Path = filepath.Clean(parsed[index].Path)
	}
	return parsed, nil
}

func parseLaunchServicesDump(dump string) []launchServicesHandler {
	var handlers []launchServicesHandler
	var current *launchServicesHandler
	finish := func() {
		if current != nil && current.Path != "" && current.ClaimsCoslash {
			handlers = append(handlers, *current)
		}
	}
	for _, line := range strings.Split(dump, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "bundle id:") {
			finish()
			current = &launchServicesHandler{}
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(lower, "path:") {
			current.Path = cleanLaunchServicesValue(trimmed[len("path:"):])
			continue
		}
		for _, key := range []string{"claimed schemes:", "scheme:", "bindings:"} {
			if strings.HasPrefix(lower, key) && containsCoslashScheme(trimmed[len(key):]) {
				current.ClaimsCoslash = true
				break
			}
		}
	}
	finish()
	return handlers
}

func cleanLaunchServicesValue(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.Index(value, " (0x"); index >= 0 {
		value = value[:index]
	}
	return strings.Trim(value, "\"'")
}

func containsCoslashScheme(value string) bool {
	for _, field := range strings.Fields(value) {
		field = strings.Trim(field, "\"'()[]{}<>,;:")
		if strings.EqualFold(field, "coslash") {
			return true
		}
	}
	return false
}

func readMacProtocolExecutable(bundle string) string {
	contents, err := os.ReadFile(filepath.Join(bundle, "Contents", "Resources", "coslash-executable"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

func unregisterMacProtocolBundle(path string) error {
	return exec.Command(launchServicesTool, "-u", path).Run()
}

func appleScriptLiteral(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
