package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRootLookupHelper(t *testing.T) {
	if os.Getenv("COSLASH_ROOT_HELPER") != "1" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	env := map[string]string{"cwd": cwd}
	for _, key := range []string{"HOME", "PWD", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG", "OPENCODE_DB", "OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_CONFIG_CONTENT"} {
		env[key] = os.Getenv(key)
	}
	data, _ := json.Marshal(env)
	if os.WriteFile(os.Getenv("COSLASH_ROOT_CAPTURE"), data, 0600) != nil {
		os.Exit(2)
	}
	fmt.Print(filepath.Join(os.Getenv("XDG_DATA_HOME"), "opencode", "opencode-dev.db"))
	os.Exit(0)
}

func TestRootLookupExcludesCallerProject(t *testing.T) {
	ambient, supplied, project := t.TempDir(), t.TempDir(), t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	// LookPath on Windows includes executable extensions.
	if err := os.WriteFile(filepath.Join(bin, "opencode.exe"), []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "capture.json")
	for key, value := range map[string]string{
		"HOME": ambient, "USERPROFILE": ambient, "PWD": project, "PATH": bin,
		"XDG_DATA_HOME": filepath.Join(supplied, "data"), "OPENCODE_DB": "",
		"OPENCODE_CONFIG":                 filepath.Join(project, "opencode.json"),
		"OPENCODE_DISABLE_PROJECT_CONFIG": "0", "COSLASH_ROOT_HELPER": "1", "COSLASH_ROOT_CAPTURE": capture,
	} {
		t.Setenv(key, value)
	}
	old := commandContext
	commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRootLookupHelper$")
	}
	t.Cleanup(func() { commandContext = old })
	got, err := effectiveDatabaseRoot(context.Background(), supplied)
	if want := filepath.Join(supplied, "data", "opencode", "opencode-dev.db"); err != nil || got != want {
		t.Fatalf("root %q, error %v, want %q", got, err, want)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	isolated := env["cwd"]
	if isolated == ambient || isolated == supplied || isolated == project || env["HOME"] != isolated || env["PWD"] != isolated || env["OPENCODE_DISABLE_PROJECT_CONFIG"] != "1" || env["OPENCODE_CONFIG"] != "" || env["OPENCODE_DB"] != "" || env["OPENCODE_CONFIG_CONTENT"] != `{"plugin":[],"mcp":{}}` {
		t.Fatal("lookup inherited caller home, project or configuration")
	}
	for key, suffix := range map[string]string{"XDG_DATA_HOME": "data", "XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state", "OPENCODE_CONFIG_DIR": filepath.Join("config", "opencode")} {
		if env[key] != filepath.Join(isolated, suffix) {
			t.Fatalf("%s escaped isolated directory", key)
		}
	}
	if _, err := os.Stat(isolated); !os.IsNotExist(err) {
		t.Fatal("lookup scratch remains")
	}
}
