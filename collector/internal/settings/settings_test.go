package settings

import (
	"strings"
	"testing"
)

func validBase() string {
	return `{
  "$schema": "https://raw.githubusercontent.com/centauri-ai/coslash/main/settings.schema.json",
  "version": 1,
  "synthesis": {"enabled": false, "backend": "claude-cli", "model": "claude-haiku-4-5"},
  "launch": {"terminal": "terminal"}
}`
}

func TestDecodeExistingSettingsWithoutRemote(t *testing.T) {
	config, err := Decode([]byte(validBase()))
	if err != nil {
		t.Fatal(err)
	}
	if config.Remote != nil {
		t.Fatalf("expected nil remote, got %+v", config.Remote)
	}
}

func TestDecodeRemoteObject(t *testing.T) {
	raw := strings.TrimSuffix(validBase(), "}") + `,
  "remote": {"id": "r_0123456789abcdef", "sshAlias": "gpu-server", "enabled": true}
}`
	config, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if config.Remote == nil || config.Remote.ID != "r_0123456789abcdef" ||
		config.Remote.SSHAlias != "gpu-server" || !config.Remote.Enabled {
		t.Fatalf("unexpected remote: %+v", config.Remote)
	}
}

func TestDecodeRemoteRejectsUnknownAndInvalid(t *testing.T) {
	cases := map[string]string{
		"unknown field": strings.TrimSuffix(validBase(), "}") + `,
  "remote": {"id": "r_0123456789abcdef", "sshAlias": "gpu", "enabled": true, "path": "x"}
}`,
		"bad alias": strings.TrimSuffix(validBase(), "}") + `,
  "remote": {"id": "r_0123456789abcdef", "sshAlias": "-bad", "enabled": true}
}`,
		"bad id": strings.TrimSuffix(validBase(), "}") + `,
  "remote": {"id": "local", "sshAlias": "gpu", "enabled": true}
}`,
		"missing enabled": strings.TrimSuffix(validBase(), "}") + `,
  "remote": {"id": "r_0123456789abcdef", "sshAlias": "gpu"}
}`,
		"option-like alias": strings.TrimSuffix(validBase(), "}") + `,
  "remote": {"id": "r_0123456789abcdef", "sshAlias": "--BatchMode=no", "enabled": true}
}`,
	}
	for name, raw := range cases {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestDecodeRemoteAllowsOmittedID(t *testing.T) {
	raw := strings.TrimSuffix(validBase(), "}") + `,
  "remote": {"sshAlias": "gpu-server", "enabled": true}
}`
	config, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if config.Remote == nil || config.Remote.ID != "" || config.Remote.SSHAlias != "gpu-server" {
		t.Fatalf("unexpected remote: %+v", config.Remote)
	}
}

func TestNewRemoteIDIsValidAndDistinct(t *testing.T) {
	a, err := NewRemoteID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewRemoteID()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidRemoteID(a) || !ValidRemoteID(b) {
		t.Fatalf("invalid ids %q %q", a, b)
	}
	if a == b {
		t.Fatal("expected distinct ids")
	}
}

func TestValidSSHAlias(t *testing.T) {
	if !ValidSSHAlias("gpu-server") || !ValidSSHAlias("a") || !ValidSSHAlias("Host_1.2") {
		t.Fatal("expected valid aliases")
	}
	for _, alias := range []string{"", "-x", "--opt", "user@host", "a/b", " has space"} {
		if ValidSSHAlias(alias) {
			t.Fatalf("expected invalid alias %q", alias)
		}
	}
}
