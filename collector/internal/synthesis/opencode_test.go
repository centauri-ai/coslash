package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

const openCodeDefaultModelFixture = `{"data":{"id":"gpt-5","providerID":"openai","package":"@opencode/ai/providers/openai"}}`

func TestOpenCodeDefaultConfigPreservesOnlySelectedModel(t *testing.T) {
	t.Setenv("OPENCODE_DB", filepath.Join(t.TempDir(), "user.db"))
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"model":"custom/chosen","plugins":["untrusted"],"instructions":["untrusted"]}`)
	var captured commandSpec
	var probeConfigPath string
	var probeConfig []byte
	config, err := openCodeDefaultConfig(context.Background(), "opencode", func(_ context.Context, spec commandSpec) ([]byte, error) {
		captured = spec
		for _, entry := range spec.env {
			if value, ok := strings.CutPrefix(entry, "OPENCODE_CONFIG="); ok {
				probeConfigPath = value
				var readErr error
				probeConfig, readErr = os.ReadFile(value)
				if readErr != nil {
					t.Fatal(readErr)
				}
			}
		}
		return []byte(`{"data":{"id":"chosen","providerID":"custom","package":"@opencode/ai/providers/openai-compatible","settings":{"baseURL":"http://localhost:1234/v1"},"modelID":"served-model","capabilities":{"tools":true},"instructions":["untrusted"],"plugins":["untrusted"]}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.bin != "opencode" || strings.Join(captured.args, " ") != "api --standalone get /api/model/default" || captured.stdin != "" {
		t.Fatalf("default probe = %+v", captured)
	}
	for _, want := range []string{"PWD=" + captured.dir, "OPENCODE_DB=" + filepath.Join(captured.dir, "opencode.db"), "OPENCODE_DISABLE_PROJECT_CONFIG=1"} {
		found := false
		for _, entry := range captured.env {
			found = found || entry == want
		}
		if !found {
			t.Fatalf("default probe missing %q: %v", want, captured.env)
		}
	}
	for _, entry := range captured.env {
		if entry != "OPENCODE_CONFIG_CONTENT=" && strings.HasPrefix(entry, "OPENCODE_CONFIG_CONTENT=") {
			t.Fatalf("default probe config should be passed by file: %q", entry)
		}
		if strings.HasPrefix(entry, "XDG_CONFIG_HOME=") || strings.HasPrefix(entry, "OPENCODE_CONFIG_DIR=") {
			t.Fatalf("default probe cannot resolve user configuration: %q", entry)
		}
	}
	if probeConfigPath == "" || filepath.Dir(probeConfigPath) != captured.dir {
		t.Fatalf("default probe config path = %q, want a private file in %q", probeConfigPath, captured.dir)
	}
	if strings.Contains(string(probeConfig), "untrusted") || !strings.Contains(string(probeConfig), `"model":"custom/chosen"`) || !strings.Contains(string(probeConfig), `"plugins":["-*","opencode.*"]`) {
		t.Fatalf("unsafe or incomplete default probe configuration: %s", probeConfig)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(config), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["model"]) != `"custom/chosen"` || !strings.Contains(string(got["providers"]), `"modelID":"served-model"`) || !strings.Contains(string(got["providers"]), `"baseURL":"http://localhost:1234/v1"`) {
		t.Fatalf("selected provider configuration was lost: %s", config)
	}
	if strings.Contains(config, "untrusted") || !strings.Contains(string(got["policies"]), `"tool.use"`) {
		t.Fatalf("unsafe synthesis configuration: %s", config)
	}
	if _, err := os.Stat(captured.dir); !os.IsNotExist(err) {
		t.Fatalf("default probe scratch survived cleanup: %v", err)
	}
}

func TestOpenCodeDefaultConfigRejectsUnavailableModel(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	large := `{"data":{"id":"chosen","providerID":"custom","package":"test","settings":{"large":"` + strings.Repeat("x", 64<<10) + `"}}}`
	for _, output := range []string{"", `{}`, `{"data":{"id":"chosen"}}`, `{"data":{"providerID":"custom"}}`, large} {
		if _, err := openCodeDefaultConfig(context.Background(), "opencode", func(context.Context, commandSpec) ([]byte, error) {
			return []byte(output), nil
		}); err == nil {
			t.Fatalf("default model probe accepted %q", output)
		}
	}
	if _, err := openCodeDefaultConfig(context.Background(), "opencode", func(context.Context, commandSpec) ([]byte, error) {
		return nil, context.Canceled
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("probe cancellation = %v", err)
	}
	t.Setenv("OPENCODE_CONFIG_CONTENT", "invalid")
	if _, err := openCodeDefaultConfig(context.Background(), "opencode", func(context.Context, commandSpec) ([]byte, error) {
		t.Fatal("probe ran with invalid inline configuration")
		return nil, nil
	}); err == nil {
		t.Fatal("invalid inline configuration was ignored")
	}
}

func TestOpenCodeDefaultProbeFailureStopsSynthesis(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	calls := 0
	runner := &CLIRunner{Backend: settings.BackendOpenCode, Model: settings.OpenCodeDefaultModel, openCodeV2: true}
	runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
		calls++
		if spec.args[0] != "api" {
			t.Fatal("synthesis ran after its default model probe failed")
		}
		return nil, context.Canceled
	}
	if _, err := runner.Run(context.Background(), "private session facts"); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("Run = %v, calls = %d", err, calls)
	}
}

func TestOpenCodeSynthesisToolDenial(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		type policy struct {
			Action   string `json:"action"`
			Resource string `json:"resource"`
			Effect   string `json:"effect"`
		}
		var config struct {
			Permission   string   `json:"permission"`
			Policies     []policy `json:"policies"`
			Experimental struct {
				Policies []policy `json:"policies"`
			} `json:"experimental"`
		}
		for _, entry := range openCodeEnv(t.TempDir(), v2) {
			if value, ok := strings.CutPrefix(entry, "OPENCODE_CONFIG_CONTENT="); ok {
				if err := json.Unmarshal([]byte(value), &config); err != nil {
					t.Fatal(err)
				}
			}
		}
		if v2 {
			if config.Permission != "" || len(config.Policies) != 1 || config.Policies[0].Action != "tool.use" || config.Policies[0].Resource != "*" || config.Policies[0].Effect != "deny" {
				t.Fatalf("v2 must deny every tool execution through policy: %+v", config)
			}
			if len(config.Experimental.Policies) != 1 || config.Experimental.Policies[0] != (policy{Action: "permission", Resource: "*", Effect: "deny"}) {
				t.Fatalf("older v2 must retain execution denial: %+v", config)
			}
		} else if config.Permission != "deny" {
			t.Fatalf("v1 must retain tool denial: %+v", config)
		}
	}
}

func TestOpenCodeCommandDiagnostic(t *testing.T) {
	output := []byte("{\"type\":\"text\",\"part\":{\"text\":\"private transcript\"}}\n" +
		"{\"type\":\"error\",\"error\":{\"name\":\"ProviderAuthError\",\"data\":{\"message\":\"login required\"}}}\n")
	if got := openCodeCommandDiagnostic(&exec.ExitError{}, output); got != "ProviderAuthError login required" {
		t.Fatalf("error event diagnostic = %q", got)
	}
	if got := openCodeCommandDiagnostic(&exec.ExitError{Stderr: []byte("EPERM\r\nprivate\x00")}, output); got != "EPERM  private" {
		t.Fatalf("stderr diagnostic = %q", got)
	}
	if got := openCodeCommandDiagnostic(errors.New("start failed"), output); got != "" {
		t.Fatalf("non-exit diagnostic = %q", got)
	}
	if got := openCodeCommandDiagnostic(&exec.ExitError{}, []byte(`{"type":"text","part":{"text":"private transcript"}}`)); got != "" {
		t.Fatalf("text event diagnostic = %q", got)
	}
	if got := boundedCommandDiagnostic([]byte(strings.Repeat("x", 4096))); len(got) != 2048 {
		t.Fatalf("diagnostic length = %d, want 2048", len(got))
	}
}

func TestParseOpenCodeSynthesisRejectsIncompleteObject(t *testing.T) {
	cases := map[string]string{
		"missing every other field": `{"outcome":"done"}`,
		"missing nextStep":          `{"goals":["ship"],"outcome":"done","keyDecisions":[]}`,
		"empty goals":               `{"goals":[],"outcome":"done","keyDecisions":[],"nextStep":"land it"}`,
		"null goals":                `{"goals":null,"outcome":"done","keyDecisions":[],"nextStep":"land it"}`,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOpenCodeSynthesis(text); err == nil {
				t.Fatalf("parseOpenCodeSynthesis(%s) succeeded, want an error", text)
			}
		})
	}
}

func TestParseOpenCodeSynthesisAcceptsCompleteObject(t *testing.T) {
	cases := map[string]string{
		"bare object":  `{"goals":["ship the fix"],"outcome":"done","keyDecisions":[],"nextStep":"land it"}`,
		"fenced":       "```json\n{\"goals\":[\"ship the fix\"],\"outcome\":\"done\",\"keyDecisions\":[],\"nextStep\":\"land it\"}\n```",
		"around prose": `Here you go: {"goals":["ship the fix"],"outcome":"done","keyDecisions":[],"nextStep":"land it"} hope that helps.`,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			synthesis, err := parseOpenCodeSynthesis(text)
			if err != nil {
				t.Fatalf("parseOpenCodeSynthesis(%s): %v", text, err)
			}
			if len(synthesis.Goals) != 1 || synthesis.Goals[0] != "ship the fix" {
				t.Fatalf("goals = %v, want [ship the fix]", synthesis.Goals)
			}
			if synthesis.NextStep != "land it" {
				t.Fatalf("nextStep = %q, want %q", synthesis.NextStep, "land it")
			}
		})
	}
}

func TestExtractJSONObject(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"nested objects", `x {"a":{"b":{"c":1}}} y`, `{"a":{"b":{"c":1}}}`},
		{"brace inside string", `{"a":"} not the end","b":1}`, `{"a":"} not the end","b":1}`},
		{"escaped quote before brace", `{"a":"say \"}\" now","b":2}`, `{"a":"say \"}\" now","b":2}`},
		{"stops after first value", `{"a":1} {"b":2}`, `{"a":1}`},
		{"no object", `just prose`, ``},
		{"unterminated", `{"a":1`, ``},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := extractJSONObject(test.input); got != test.want {
				t.Fatalf("extractJSONObject(%s) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestCleanupScratchRemovesOnlyAbandonedDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	root := SynthesisCwd()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(root, openCodeScratchPrefix+"stale")
	live := filepath.Join(root, openCodeScratchPrefix+"live")
	staleCursor := filepath.Join(root, cursorScratchPrefix+"stale")
	liveCursor := filepath.Join(root, cursorScratchPrefix+"live")
	stalePi := filepath.Join(root, ".pi-stale")
	livePi := filepath.Join(root, ".pi-live")
	unrelated := filepath.Join(root, "summaries-ish")
	for _, directory := range []string{stale, live, staleCursor, liveCursor, stalePi, livePi, unrelated} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * scratchMaxAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staleCursor, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stalePi, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unrelated, old, old); err != nil {
		t.Fatal(err)
	}

	if err := CleanupScratch(); err != nil {
		t.Fatalf("CleanupScratch: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("abandoned scratch directory survived the sweep")
	}
	if _, err := os.Stat(staleCursor); !os.IsNotExist(err) {
		t.Error("abandoned Cursor scratch directory survived the sweep")
	}
	if _, err := os.Stat(stalePi); !os.IsNotExist(err) {
		t.Error("abandoned Pi scratch directory survived sweep")
	}
	if _, err := os.Stat(livePi); err != nil {
		t.Errorf("in-flight Pi scratch directory was swept: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("in-flight scratch directory was swept: %v", err)
	}
	if _, err := os.Stat(liveCursor); err != nil {
		t.Errorf("in-flight Cursor scratch directory was swept: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated directory was swept: %v", err)
	}
}
