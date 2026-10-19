package main

import (
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

func ensurePiExtensionIfDetected() error {
	if _, err := vendors.PiExecutable(); err == nil {
		return pi.EnsureExtension()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	info, err := os.Stat(filepath.Join(home, ".pi"))
	if err != nil || !info.IsDir() {
		return nil
	}
	return pi.EnsureExtension()
}
