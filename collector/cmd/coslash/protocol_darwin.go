//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const macProtocolBundleName = "coSlash Local Pairing Handler.app"

func registerProtocolHandler() error {
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
	parent := filepath.Join(home, "Library", "Application Support", "coSlash Local")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	bundle := filepath.Join(parent, macProtocolBundleName)
	marker := filepath.Join(bundle, "Contents", "Resources", "coslash-executable")
	if contents, err := os.ReadFile(marker); err == nil && string(contents) == executable {
		return exec.Command("/usr/bin/open", "-a", bundle).Run()
	}
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
<key>CFBundleIdentifier</key><string>io.coslash.local.pairing-handler</string>
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
	if err := os.RemoveAll(bundle); err != nil {
		return err
	}
	if err := os.Rename(app, bundle); err != nil {
		return err
	}
	if err := exec.Command("/usr/bin/open", "-a", bundle).Run(); err != nil {
		return err
	}
	return nil
}

func appleScriptLiteral(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func unsupportedProtocolHandler() error { return errors.New("protocol handler is unavailable") }
