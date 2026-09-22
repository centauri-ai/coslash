package main

import (
	"os"

	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
)

func protectTokenDirectory(path string) error {
	return windowsprivate.ProtectDirectory(path, "token")
}

func protectTokenFile(path string, file *os.File) error {
	return windowsprivate.ProtectFile(path, file, "token")
}
