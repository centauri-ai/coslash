package synthesis

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestGrokSynthesis(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{settings.GrokSynthesisModel, settings.GrokDefaultModel} {
		t.Run(model, func(t *testing.T) {
			runner := &CLIRunner{Backend: settings.BackendGrok, Bin: "grok", Model: model, Timeout: time.Second}
			if runner.VendorName() != "grok" {
				t.Fatal("Grok vendor identity missing")
			}
			runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
				want := []string{
					"--prompt-file", filepath.Join(spec.dir, "prompt.txt"),
					"--json-schema", synthesisSchema,
					"--max-turns", "1",
					"--no-subagents",
					"--tools", "",
					"--deny", "*",
					"--permission-mode", "dontAsk",
					"--disallowed-tools", "run_terminal_cmd,search_replace,web_search,web_fetch",
					"--rules", systemPrompt,
				}
				if model != settings.GrokDefaultModel {
					want = append(want, "--model", model)
				}
				if model == settings.GrokSynthesisModel {
					want = append(want, "--effort", "high")
				}
				if spec.bin != "grok" || !slices.Equal(spec.args, want) {
					t.Fatalf("command=%#v", spec)
				}
				if spec.stdin != "" || strings.Contains(strings.Join(spec.args, " "), "private session facts") {
					t.Fatal("session facts must stay in the prompt file")
				}
				body, err := os.ReadFile(filepath.Join(spec.dir, "prompt.txt"))
				if err != nil || string(body) != "private session facts" {
					t.Fatalf("prompt=%q err=%v", body, err)
				}
				grokHome := filepath.Join(spec.dir, "home")
				if !slices.Contains(spec.env, "GROK_HOME="+grokHome) || !slices.Contains(spec.env, "GROK_MEMORY=0") {
					t.Fatalf("env=%q", spec.env)
				}
				authPath := filepath.Join(grokHome, "auth.json")
				body, err = os.ReadFile(authPath)
				if err != nil || string(body) != `{"token":"secret"}` {
					t.Fatalf("login was not retained: %v", err)
				}
				if runtime.GOOS != "windows" {
					link, err := os.Readlink(authPath)
					if err != nil || link != filepath.Join(home, "auth.json") {
						t.Fatalf("auth link=%q err=%v", link, err)
					}
				}
				if _, err := os.Stat(filepath.Join(grokHome, "sessions")); err == nil {
					t.Fatal("scratch home must not start with the user's sessions")
				}
				return []byte(`{"text":"{\"goals\":[\"Ship Grok synthesis\"],\"outcome\":\"Backend added\",\"keyDecisions\":[],\"nextStep\":\"Review\"}"}`), nil
			}
			got, err := runner.Run(context.Background(), "private session facts")
			if err != nil || got.Synthesis.Outcome != "Backend added" {
				t.Fatalf("got=%#v err=%v", got, err)
			}
			if got.Usage.Coverage != "unknown" {
				t.Fatalf("usage=%#v", got.Usage)
			}
			entries, err := os.ReadDir(SynthesisCwd())
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("scratch leaked: %v", entries)
			}
		})
	}
}

func TestParseGrokUsage(t *testing.T) {
	output := []byte(`{
		"text":"{\"goals\":[\"Ship\"],\"outcome\":\"Done\",\"keyDecisions\":[],\"nextStep\":\"Review\"}",
		"usage":{"input_tokens":7210,"cache_read_input_tokens":41000,"cache_creation_input_tokens":0,"output_tokens":1893,"reasoning_tokens":412,"total_tokens":50103},
		"modelUsage":{"grok-4.7":{"inputTokens":7210,"outputTokens":1893,"cacheReadInputTokens":41000,"costUSD":9.9}},
		"total_cost_usd":9.9,
		"total_cost_usd_ticks":126890500
	}`)
	got := parseGrokUsage(output, settings.GrokDefaultModel)
	used := got.Tokens["grok-4.7"]
	if got.Coverage != "complete" || got.ReportedCostMicroUSD == nil || *got.ReportedCostMicroUSD != 12689 || used.InputTokens != 7210 || used.OutputTokens != 1893 || used.CacheReadInputTokens != 41000 || used.CacheCreationInputTokens != 0 {
		t.Fatalf("usage=%#v tokens=%#v", got, used)
	}
	incomplete := append([]byte(nil), output...)
	incomplete = bytes.Replace(incomplete, []byte(`"total_cost_usd_ticks":126890500`), []byte(`"usage_is_incomplete":true`), 1)
	got = parseGrokUsage(incomplete, settings.GrokSynthesisModel)
	if got.Coverage != "partial" || got.ReportedCostMicroUSD != nil || got.Tokens["grok-4.7"].InputTokens != 7210 {
		t.Fatalf("incomplete=%#v", got)
	}
	if got := parseGrokUsage([]byte(`{"text":"ok"}`), settings.GrokSynthesisModel); got.Coverage != "unknown" || got.ReportedCostMicroUSD != nil || got.Tokens != nil {
		t.Fatalf("missing spend=%#v", got)
	}
	negative := parseGrokUsage([]byte(`{"usage":{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"total_cost_usd_ticks":-1}`), settings.GrokSynthesisModel)
	if negative.ReportedCostMicroUSD != nil || negative.Tokens[settings.GrokSynthesisModel].InputTokens != 1 {
		t.Fatalf("negative cost=%#v", negative)
	}
}

func TestGrokSynthesisResolvesRelativeHomes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("coslash", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("grokhome", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("grokhome", "auth.json"), []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSLASH_HOME", "coslash")
	t.Setenv("GROK_HOME", "grokhome")
	runner := &CLIRunner{Backend: settings.BackendGrok, Bin: "grok", Model: settings.GrokSynthesisModel, Timeout: time.Second}
	runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
		prompt := spec.args[slices.Index(spec.args, "--prompt-file")+1]
		if !filepath.IsAbs(spec.dir) || !filepath.IsAbs(prompt) || filepath.Dir(prompt) != spec.dir {
			t.Fatalf("scratch=%q prompt=%q", spec.dir, prompt)
		}
		grokHome := strings.TrimPrefix(spec.env[0], "GROK_HOME=")
		if !filepath.IsAbs(grokHome) || filepath.Dir(grokHome) != spec.dir {
			t.Fatalf("env=%q", spec.env)
		}
		if runtime.GOOS != "windows" {
			link, err := os.Readlink(filepath.Join(grokHome, "auth.json"))
			if err != nil || link != filepath.Join(root, "grokhome", "auth.json") {
				t.Fatalf("auth link=%q err=%v", link, err)
			}
		}
		body, err := os.ReadFile(filepath.Join(grokHome, "auth.json"))
		if err != nil || string(body) != `{"token":"secret"}` {
			t.Fatalf("login=%q err=%v", body, err)
		}
		return []byte(`{"text":"{\"goals\":[\"Ship\"],\"outcome\":\"Done\",\"keyDecisions\":[],\"nextStep\":\"Review\"}"}`), nil
	}
	if _, err := runner.Run(context.Background(), "facts"); err != nil {
		t.Fatal(err)
	}
}

func TestGrokStructuredOutput(t *testing.T) {
	for _, output := range []string{
		`{"structuredOutput":{"goals":["Ship"],"outcome":"Done","keyDecisions":[],"nextStep":"Review"},"text":"non-JSON commentary"}`,
		`{"text":"{\"goals\":[\"Ship\"],\"outcome\":\"Done\",\"keyDecisions\":[],\"nextStep\":\"Review\"}"}`,
	} {
		got, err := parseGrokSynthesis([]byte(output))
		if err != nil || got.Outcome != "Done" {
			t.Fatalf("structured synthesis = %#v, %v", got, err)
		}
	}
	if _, err := parseGrokSynthesis([]byte(`{"structuredOutput":{"outcome":"incomplete"}}`)); err == nil {
		t.Fatal("accepted incomplete structured output")
	}
}

func TestGrokSynthesisFailure(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	for _, tc := range []struct {
		name, output string
		err          error
		want         string
	}{
		{"missing fields", `{"text":"{\"outcome\":\"looks valid\"}"}`, nil, "synthesis result"},
		{"provider auth", "private-token", &exec.ExitError{}, "verify CLI authentication and the selected model"},
		{"missing CLI", "", exec.ErrNotFound, "not installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &CLIRunner{Backend: settings.BackendGrok, Bin: "grok", Model: settings.GrokSynthesisModel, Timeout: time.Second, exec: func(context.Context, commandSpec) ([]byte, error) {
				return []byte(tc.output), tc.err
			}}
			_, err := r.Run(context.Background(), "facts")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestGrokRunnerPlatformAvailability(t *testing.T) {
	runner, err := NewRunner(settings.SynthesisSettings{Enabled: true, Backend: settings.BackendGrok, Model: settings.GrokSynthesisModel})
	if vendors.GrokSynthesisSupported() {
		if err != nil || runner == nil {
			t.Fatalf("supported Grok runner unavailable: %v", err)
		}
	} else if err == nil || runner != nil || !strings.Contains(err.Error(), "macOS") {
		t.Fatalf("unsupported Grok runner = %v, %v", runner, err)
	}
}

func TestGrokSynthesisSettings(t *testing.T) {
	if settings.BackendExecutable(settings.BackendGrok) == "" && vendors.GrokSynthesisSupported() {
		t.Fatal("supported Grok synthesis has no executable")
	}
	if settings.BackendExecutable(settings.BackendGrok) != "" && !vendors.GrokSynthesisSupported() {
		t.Fatal("unsupported Grok synthesis is executable")
	}
}
