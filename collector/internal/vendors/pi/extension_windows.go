package pi

import (
	"os"
	"path/filepath"

	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
)

func protectExtensionDirectories(extensionDir string) error {
	if err := windowsprivate.ProtectDirectory(extensionDir, "Pi extension"); err != nil {
		return err
	}
	home, err := filepath.Abs(stateHome())
	if err != nil {
		return err
	}
	for _, dir := range []string{home, filepath.Join(home, "pi-runtime"), filepath.Join(home, "pi-history")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := windowsprivate.ProtectDirectory(dir, "Pi runtime"); err != nil {
			return err
		}
	}
	return nil
}

func protectExtensionFile(file *os.File) error {
	return windowsprivate.ProtectFile(file.Name(), file, "Pi extension")
}
