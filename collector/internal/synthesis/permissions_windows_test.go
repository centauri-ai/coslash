package synthesis

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/windowstest"
)

func assertPrivateSynthesisPath(t *testing.T, path string, directory bool) {
	t.Helper()
	windowstest.AssertPrivateACL(t, path, directory)
}
