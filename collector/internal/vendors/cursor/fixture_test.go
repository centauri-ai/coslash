package cursor

import (
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func cursorTestGlobalStorage(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return GlobalStorage(home)
}

func selectForTest(t *testing.T, source vendors.ReadSource, files []string, since int64, metadata *vendors.SessionMetadata) []string {
	t.Helper()
	selected, err := selectCursorFiles(t.Context(), source, files, since, metadata)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}
