package cursor

import (
	"path/filepath"
	"testing"
)

func cursorTestGlobalStorage(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return GlobalStorage(home)
}
