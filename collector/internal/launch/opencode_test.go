package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func stubOpenCodeMajor(t *testing.T, major int) {
	t.Helper()
	original := openCodeMajorVersion
	openCodeMajorVersion = func() (int, error) { return major, nil }
	t.Cleanup(func() { openCodeMajorVersion = original })
}

func TestOpenCodeV2ReviewIsolatedAndReadOnly(t *testing.T) {
	stubOpenCodeMajor(t, 2)
	t.Setenv("COSLASH_HOME", t.TempDir())
	worktree := t.TempDir()
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reviewCLICommand("opencode", worktree, "review title", "--untrusted prompt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(got.cleanup)
	if !reflect.DeepEqual(got.args, []string{"run", "--standalone", "--title", "review title"}) || got.dir == worktree {
		t.Fatalf("review command = %#v", got)
	}
	env := map[string]string{}
	for _, entry := range got.env {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	if env["PWD"] != got.dir || env["OPENCODE_CONFIG"] != "" || env["OPENCODE_DISABLE_PROJECT_CONFIG"] != "1" || env["XDG_CONFIG_HOME"] != filepath.Join(got.dir, "config") {
		t.Fatalf("review environment = %#v", env)
	}
	var config struct {
		Plugins     []string `json:"plugins"`
		Permissions []struct {
			Action   string `json:"action"`
			Resource string `json:"resource"`
			Effect   string `json:"effect"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(env["OPENCODE_CONFIG_CONTENT"]), &config); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.Plugins, []string{"-*", "opencode.*"}) || len(config.Permissions) != 5 || config.Permissions[0].Action != "*" || config.Permissions[0].Effect != "deny" {
		t.Fatalf("review config = %#v", config)
	}
	for _, rule := range config.Permissions[1:] {
		if rule.Effect != "allow" || (rule.Action != "read" && rule.Action != "glob" && rule.Action != "grep" && rule.Action != "external_directory") {
			t.Fatalf("unexpected review capability: %#v", rule)
		}
		if (rule.Action == "read" || rule.Action == "external_directory") && rule.Resource != filepath.ToSlash(worktree)+"/*" {
			t.Fatalf("review access escapes the worktree: %#v", rule)
		}
	}
	if !strings.HasPrefix(got.stdin, "--untrusted prompt\n") || !strings.Contains(got.stdin, strconv.Quote(worktree)) {
		t.Fatalf("review stdin = %q", got.stdin)
	}
	got.cleanup()
	if _, err := os.Stat(got.dir); !os.IsNotExist(err) {
		t.Fatalf("review scratch survived cleanup: %v", err)
	}
}
