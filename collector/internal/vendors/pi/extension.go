package pi

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

//go:embed coslash-extension.ts
var extensionSource []byte

func ExtensionPath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), "extensions", "coslash-extension.ts"), nil
}

// EnsureExtension installs through Pi's native extension discovery, preserving settings.
func EnsureExtension() error {
	if !vendors.PiSupported() {
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
			return nil
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
	file, err := os.CreateTemp(filepath.Dir(target), ".coslash-extension-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
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

type ExtensionHealth struct {
	Path            string
	Installed       bool
	RestartRequired bool
	Err             error
}

func ExtensionDiagnostics() ExtensionHealth {
	if !vendors.PiSupported() {
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
