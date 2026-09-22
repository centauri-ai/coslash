package settings

import (
	"os"

	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
)

func readSettingsFile(path string) ([]byte, error) {
	return windowsprivate.ReadFile(path, "settings.json", "settings")
}

func protectSettingsDirectory(path string) error {
	return windowsprivate.ProtectDirectory(path, "settings")
}

func protectSettingsFile(path string, file *os.File) error {
	return windowsprivate.ProtectFile(path, file, "settings.json")
}
