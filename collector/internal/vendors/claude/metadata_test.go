package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMetadataUsesUserConfigDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("APPDATA", filepath.Join(home, "config"))

	metadata, err := LoadMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Lookup("desktop-session"); got != nil {
		t.Fatalf("missing desktop directory metadata = %#v, want nil", got)
	}

	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(
		config, "Claude", "claude-code-sessions", "project", "desktop-session", "metadata.json",
	)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte(`{"cliSessionId":"desktop-session","title":"Desktop title"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	metadata, err = LoadMetadata()
	if err != nil {
		t.Fatal(err)
	}
	got := metadata.Lookup("desktop-session")
	if got == nil || got.Name != "Desktop title" {
		t.Fatalf("desktop metadata = %#v, want title from user config directory", got)
	}
}
