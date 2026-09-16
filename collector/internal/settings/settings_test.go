package settings

import (
	"os"
	"strings"
	"testing"
)

func TestDecodeRemoteExecutableOverridesAndPreservesVersionOneSettings(t *testing.T) {
	legacy, err := Decode([]byte(validSettings(`"remote":{"id":"r_0123456789abcdef","sshAlias":"agent-box","enabled":true}`)))
	if err != nil {
		t.Fatalf("decode legacy settings: %v", err)
	}
	if legacy.Remote == nil || legacy.Remote.Executables != nil {
		t.Fatalf("legacy remote settings = %#v", legacy.Remote)
	}

	config, err := Decode([]byte(validSettings(`"remote":{"id":"r_0123456789abcdef","sshAlias":"agent-box","enabled":true,"executables":{"claude":"/opt/claude/bin/claude","codex":"~/bin/codex","opencode":"/opt/opencode/bin/opencode"}}`)))
	if err != nil {
		t.Fatalf("decode executable overrides: %v", err)
	}
	if got := config.Remote.ExecutableForAgent("claude"); got != "/opt/claude/bin/claude" {
		t.Fatalf("Claude executable = %q", got)
	}
	if got := config.Remote.ExecutableForAgent("codex"); got != "~/bin/codex" {
		t.Fatalf("Codex executable = %q", got)
	}
	if got := config.Remote.ExecutableForAgent("opencode"); got != "/opt/opencode/bin/opencode" {
		t.Fatalf("OpenCode executable = %q", got)
	}
}

func TestDecodeRejectsInvalidRemoteExecutableOverrides(t *testing.T) {
	for _, path := range []string{"", "/", "bin/codex", "~codex", "~/", "/bin/codex\n"} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			_, err := Decode([]byte(validSettings(`"remote":{"id":"r_0123456789abcdef","sshAlias":"agent-box","enabled":true,"executables":{"codex":` + quoteJSON(path) + `}}`)))
			if err == nil {
				t.Fatalf("Decode accepted %q", path)
			}
		})
	}
}

func TestSaveRoundTripsRemoteExecutableOverrides(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	config, err := Decode([]byte(validSettings(`"remote":{"id":"r_0123456789abcdef","sshAlias":"agent-box","enabled":true,"executables":{"claude":"~/bin/claude","codex":"/opt/codex/bin/codex"}}`)))
	if err != nil {
		t.Fatal(err)
	}
	store := Open()
	if err := store.Save(config); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode saved settings: %v", err)
	}
	if got := decoded.Remote.ExecutableForAgent("claude"); got != "~/bin/claude" {
		t.Fatalf("Claude executable after round trip = %q", got)
	}
	if got := decoded.Remote.ExecutableForAgent("codex"); got != "/opt/codex/bin/codex" {
		t.Fatalf("Codex executable after round trip = %q", got)
	}
}

func validSettings(extra string) string {
	return `{"$schema":"` + SchemaURL + `","version":1,"synthesis":{"enabled":false,"backend":"claude-cli","model":"claude-haiku-4-5"},"appearance":{"theme":"light"},"launch":{"terminal":"terminal"},` + extra + `}`
}

func quoteJSON(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r", `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
