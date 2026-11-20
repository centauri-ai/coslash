package search

import (
	"os"

	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
)

func protectDirectory(path string) error { return windowsprivate.ProtectDirectory(path, "search") }
func protectFile(path string, f *os.File) error {
	return windowsprivate.ProtectFile(path, f, "search index")
}
