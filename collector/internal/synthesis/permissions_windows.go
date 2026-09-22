package synthesis

import (
	"os"

	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
)

func protectSynthesisDirectory(path string) error {
	return windowsprivate.ProtectDirectory(path, "synthesis")
}

func protectSynthesisFile(path string, file *os.File) error {
	return windowsprivate.ProtectFile(path, file, "synthesis cache")
}

func readSynthesisFile(path string) ([]byte, error) {
	return windowsprivate.ReadFile(path, "synthesis cache", "synthesis")
}
