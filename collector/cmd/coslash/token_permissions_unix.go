//go:build !windows

package main

import (
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/atomicfile"
)

func protectTokenDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func writeTokenFile(home, token string) error {
	return atomicfile.Write(filepath.Join(home, "token"), []byte(token+"\n"), 0o600, nil)
}
