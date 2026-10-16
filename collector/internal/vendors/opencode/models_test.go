package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModelProbeUsesVersionSpecificCLIAndIsolatesV2Database(t *testing.T) {
	for _, version := range []string{"1.18.33", "2.0.30"} {
		t.Run(version, func(t *testing.T) {
			setOpenCodeVersionDetectorForTest(t, func() (string, error) { return version, nil })
			output := "opencode/free\n{\n\"cost\":{\"input\":0,\"output\":0},\"capabilities\":{\"output\":{\"text\":true}}\n}\n"
			wantArgs := []string{"models", "opencode", "--verbose", "--pure"}
			if version[0] == '2' {
				output = `{"data":[{"id":"free","providerID":"opencode","enabled":true,"capabilities":{"output":["text"]},"cost":[{"input":0,"output":0,"cache":{"read":0,"write":0}}]}]}`
				wantArgs = []string{"api", "--standalone", "get", "/api/model"}
			}
			t.Setenv("COSLASH_TEST_MODEL_OUTPUT", output)
			t.Setenv("OPENCODE_DB", filepath.Join(t.TempDir(), "user.db"))
			original := commandContext
			var command *exec.Cmd
			var gotArgs []string
			commandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
				gotArgs = args
				command = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelProbeHelper$")
				return command
			}
			t.Cleanup(func() { commandContext = original })
			if got := loadModels(); !reflect.DeepEqual(got, []string{"opencode/free"}) || !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Fatalf("models = %v, command = %v", got, gotArgs)
			}
			env := map[string]string{}
			for _, entry := range command.Env {
				key, value, _ := strings.Cut(entry, "=")
				env[key] = value
			}
			if env["PWD"] != command.Dir || env["OPENCODE_DISABLE_PROJECT_CONFIG"] != "1" {
				t.Fatalf("unsafe model probe environment: %#v", env)
			}
			if version[0] == '2' && (env["OPENCODE_DB"] != filepath.Join(command.Dir, "opencode.db") || env["XDG_CONFIG_HOME"] != command.Dir || env["OPENCODE_CONFIG_DIR"] != command.Dir) {
				t.Fatal("v2 model probe inherited user configuration or database")
			}
			if _, err := os.Stat(command.Dir); !os.IsNotExist(err) {
				t.Fatalf("model probe scratch survived cleanup: %v", err)
			}
		})
	}
}

func TestModelProbeHelper(t *testing.T) {
	if output := os.Getenv("COSLASH_TEST_MODEL_OUTPUT"); output != "" {
		fmt.Print(output)
		os.Exit(0)
	}
}

func TestV2ModelsRequireKnownFreeTextPricingInEveryTier(t *testing.T) {
	free := map[string]any{
		"id": "free", "providerID": "opencode", "enabled": true, "status": "active",
		"capabilities": map[string]any{"output": []string{"text"}},
		"cost":         []any{map[string]any{"input": 0, "output": 0, "cache": map[string]any{"read": 0, "write": 0}}},
	}
	for _, kind := range []string{"free", "paid", "paid-cache", "paid-tier", "unknown", "null", "disabled", "deprecated", "image", "other-provider"} {
		t.Run(kind, func(t *testing.T) {
			data, _ := json.Marshal(free)
			var model map[string]any
			if err := json.Unmarshal(data, &model); err != nil {
				t.Fatal(err)
			}
			tier := model["cost"].([]any)[0].(map[string]any)
			switch kind {
			case "paid":
				tier["output"] = 1
			case "paid-cache":
				tier["cache"].(map[string]any)["read"] = 1
			case "paid-tier":
				model["cost"] = append(model["cost"].([]any), map[string]any{"input": 1, "output": 1})
			case "unknown":
				delete(tier, "input")
			case "null":
				tier["output"] = nil
			case "disabled":
				model["enabled"] = false
			case "deprecated":
				model["status"] = "deprecated"
			case "image":
				model["capabilities"] = map[string]any{"output": []string{"image"}}
			case "other-provider":
				model["providerID"] = "paid"
			}
			data, _ = json.Marshal(map[string]any{"data": []any{model}})
			want := []string{}
			if kind == "free" {
				want = []string{"opencode/free"}
			}
			if got := parseV2Models(data); !reflect.DeepEqual(got, want) {
				t.Fatalf("models = %v, want %v", got, want)
			}
		})
	}
	if got := parseV2Models([]byte(`{"data":`)); len(got) != 0 {
		t.Fatalf("malformed model response = %v", got)
	}
}

func TestLegacyVerboseModelsStillSupported(t *testing.T) {
	output := `opencode/free
{
"cost":{"input":0,"output":0},"capabilities":{"output":{"text":true}},"status":"active"
}
opencode/paid
{
"cost":{"input":1,"output":1},"capabilities":{"output":{"text":true}},"status":"active"
}
`
	if got := parseVerboseModels([]byte(output)); !reflect.DeepEqual(got, []string{"opencode/free"}) {
		t.Fatalf("legacy models = %v", got)
	}
	if got := parseVerboseModels([]byte(strings.Repeat(output, modelsLimit+1))); len(got) != modelsLimit {
		t.Fatalf("legacy model limit = %d", len(got))
	}
}
