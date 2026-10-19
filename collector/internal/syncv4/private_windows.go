//go:build windows

package syncv4

import (
	"os"

	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
)

func protectQueueDirectory(path string) error {
	return windowsprivate.ProtectDirectory(path, "v4 sync")
}
func protectQueueFile(path string, file *os.File) error {
	return windowsprivate.ProtectFile(path, file, "v4 sync queue")
}
func readQueueFile(path string) ([]byte, error) {
	return windowsprivate.ReadFile(path, "v4 sync queue", "v4 sync")
}
