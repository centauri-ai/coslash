package pi

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsExtensionInstallAndManagedReplacement(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("COSLASH_HOME", t.TempDir())
	if err := ensureExtension(); err != nil {
		t.Fatal(err)
	}
	target, err := ExtensionPath()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(installed, extensionSource) {
		t.Fatalf("installed extension = %d bytes, %v", len(installed), err)
	}
	for _, name := range []string{"pi-runtime", "pi-history"} {
		if info, err := os.Stat(filepath.Join(stateHome(), name)); err != nil || !info.IsDir() {
			t.Fatalf("private runtime directory %s: %v", name, err)
		}
	}
	if err := os.WriteFile(target, []byte("// managed by coSlash; old version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureExtension(); err != nil {
		t.Fatal(err)
	}
	installed, err = os.ReadFile(target)
	if err != nil || !bytes.Equal(installed, extensionSource) {
		t.Fatalf("managed replacement = %d bytes, %v", len(installed), err)
	}
}
