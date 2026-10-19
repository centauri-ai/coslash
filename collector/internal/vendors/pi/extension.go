package pi

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

//go:embed coslash-extension.ts
var extensionSource []byte

const MinimumRuntimeVersion = "0.99.1"

var stableRelease = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// RuntimeSupported accepts stable releases from the tested 0.99.1 baseline onward.
func RuntimeSupported(version string) bool {
	parts := stableRelease.FindStringSubmatch(version)
	if parts == nil {
		return false
	}
	return parts[1] != "0" || len(parts[2]) > 2 || (parts[2] == "99" && parts[3] != "0")
}

func ExtensionPath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), "extensions", "coslash-extension.ts"), nil
}

// EnsureExtension installs through Pi's native extension discovery, preserving settings.
func EnsureExtension() error {
	if !vendors.PiCollectionSupported() {
		return nil
	}
	return ensureExtension()
}
func ensureExtension() error {
	target, err := ExtensionPath()
	if err != nil {
		return err
	}
	current, err := os.ReadFile(target)
	if err == nil {
		if bytes.Equal(current, extensionSource) {
			return protectInstalledExtension(target)
		}
		if !bytes.HasPrefix(current, []byte("// managed by coSlash;")) {
			return fmt.Errorf("refusing to overwrite unmanaged Pi extension %s", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := protectExtensionDirectories(filepath.Dir(target)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".coslash-extension-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if err := protectExtensionFile(file); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(extensionSource); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}

func protectInstalledExtension(path string) error {
	if err := protectExtensionDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return protectExtensionFile(file)
}

type ExtensionHealth struct {
	Path            string
	Installed       bool
	RestartRequired bool
	Err             error
}

func ExtensionDiagnostics() ExtensionHealth {
	if !vendors.PiCollectionSupported() {
		return ExtensionHealth{}
	}
	return extensionDiagnostics()
}
func extensionDiagnostics() ExtensionHealth {
	target, err := ExtensionPath()
	health := ExtensionHealth{Path: target, Err: err}
	if err != nil {
		return health
	}
	current, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return health
	}
	if err != nil {
		health.Err = err
		return health
	}
	if !bytes.Equal(current, extensionSource) {
		health.Err = errors.New("installed extension differs from the coSlash extension")
		return health
	}
	health.Installed = true
	info, err := os.Stat(target)
	if err != nil {
		health.Err = err
		return health
	}
	records, err := runtimeRecords()
	if err != nil {
		health.Err = err
		return health
	}
	for _, record := range records {
		if ownerState(record) == "live" && record.Record.StartedAtMs < info.ModTime().UnixMilli() {
			health.RestartRequired = true
			break
		}
	}
	return health
}
