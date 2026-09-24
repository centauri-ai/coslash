package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestCLIRunnerRunsCursorReadOnlyWithIsolatedData(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("COSLASH_HOME", t.TempDir())
	var captured commandSpec
	var configData []byte
	var configErr error
	created, err := NewRunner(settings.SynthesisSettings{
		Enabled: true,
		Backend: settings.BackendCursor,
		Model:   "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := created.(*CLIRunner)
	runner.Timeout = time.Second
	runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
		captured = spec
		configData, configErr = os.ReadFile(filepath.Join(spec.dir, ".cursor", "cli.json"))
		return []byte(`{"type":"result","subtype":"success","is_error":false,"result":"{\"goals\":[\"Ship Cursor synthesis\"],\"outcome\":\"Backend added\",\"keyDecisions\":[],\"nextStep\":\"Open a PR\"}"}`), nil
	}

	got, err := runner.Run(context.Background(), "normalized session facts")
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "Backend added" {
		t.Fatalf("outcome = %q, want Backend added", got.Outcome)
	}
	if captured.bin != "cursor-agent" {
		t.Fatalf("bin = %q, want cursor-agent", captured.bin)
	}
	wantArgs := []string{
		"-p", "--mode", "ask", "--sandbox", "enabled", "--trust",
		"--model", "auto", "--output-format", "json",
	}
	if !slices.Equal(captured.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", captured.args, wantArgs)
	}
	if filepath.Dir(captured.dir) != SynthesisCwd() || !strings.HasPrefix(filepath.Base(captured.dir), ".cursor-") {
		t.Fatalf("dir = %q, want isolated child of %q", captured.dir, SynthesisCwd())
	}
	if !strings.Contains(captured.stdin, systemPrompt) ||
		!strings.Contains(captured.stdin, "normalized session facts") {
		t.Fatalf("stdin does not contain the synthesis prompt and facts: %q", captured.stdin)
	}
	if strings.Contains(strings.Join(captured.args, " "), "normalized session facts") {
		t.Fatal("session facts leaked into argv")
	}
	if len(captured.env) != 1 || !strings.HasPrefix(captured.env[0], "CURSOR_DATA_DIR=") {
		t.Fatalf("env = %#v, want one isolated CURSOR_DATA_DIR", captured.env)
	}
	scratch := strings.TrimPrefix(captured.env[0], "CURSOR_DATA_DIR=")
	if scratch != captured.dir {
		t.Fatalf("CURSOR_DATA_DIR = %q, want workspace %q", scratch, captured.dir)
	}
	if configErr != nil {
		t.Fatalf("read Cursor permissions: %v", configErr)
	}
	var config struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(configData, &config); err != nil {
		t.Fatalf("decode Cursor permissions: %v", err)
	}
	wantDeny := []string{"Read(*)", "Read(**)", "Shell(*)", "Write(*)", "WebFetch(*)", "Mcp(*)"}
	if len(config.Permissions.Allow) != 0 || !slices.Equal(config.Permissions.Deny, wantDeny) {
		t.Fatalf("permissions = %#v, want empty allow and deny %#v", config.Permissions, wantDeny)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("scratch dir was not removed: %v", err)
	}
}

func TestOpenCodeRunnerCachesVersionAcrossSettingsSaves(t *testing.T) {
	original := cachedOpenCodeV2
	t.Cleanup(func() { cachedOpenCodeV2 = original })
	for _, v2 := range []bool{true, false} {
		t.Run(fmt.Sprint(v2), func(t *testing.T) {
			probes := 0
			cachedOpenCodeV2 = newOpenCodeV2Detector(func(bin string) bool {
				if bin != "opencode" {
					t.Fatalf("version probe binary = %q", bin)
				}
				probes++
				return v2
			})
			config := settings.SynthesisSettings{Enabled: true, Backend: settings.BackendOpenCode, Model: settings.OpenCodeSynthesisModel}
			for range 2 {
				created, err := NewRunner(config)
				if err != nil {
					t.Fatal(err)
				}
				if created.(*CLIRunner).openCodeV2 != v2 {
					t.Fatalf("OpenCode v2 = %v, want %v", created.(*CLIRunner).openCodeV2, v2)
				}
			}
			if probes != 1 {
				t.Fatalf("version probes = %d, want 1", probes)
			}
		})
	}
}

func TestOpenCodeRunnerUsesVersionedRunFlags(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, test := range []struct {
		name       string
		v2         bool
		model      string
		wantArgs   []string
		wantConfig string
	}{
		{"v1", false, settings.OpenCodeSynthesisModel, []string{"run", "--dir", SynthesisCwd(), "--model", settings.OpenCodeSynthesisModel, "--variant", "high", "--format", "json", "--pure", "--", systemPrompt + jsonInstruction}, openCodeConfigContent},
		{"v2", true, settings.OpenCodeSynthesisModel, []string{"run", "--standalone", "--model", settings.OpenCodeSynthesisModel + "#high", "--format", "json", "--", systemPrompt + jsonInstruction}, openCodeV2ConfigContent},
		{"v2 default", true, settings.OpenCodeDefaultModel, []string{"run", "--standalone", "--format", "json", "--", systemPrompt + jsonInstruction}, openCodeV2ConfigContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			var captured commandSpec
			runner := &CLIRunner{Backend: settings.BackendOpenCode, Bin: "opencode", Model: test.model, Timeout: time.Second, openCodeV2: test.v2}
			runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
				captured = spec
				return []byte("{\"type\":\"text\",\"part\":{\"id\":\"answer\",\"text\":\"{\\\"goals\\\":[\\\"ship\\\"],\\\"outcome\\\":\\\"done\\\",\\\"keyDecisions\\\":[],\\\"nextStep\\\":\\\"review\\\"}\"}}\n"), nil
			}
			if _, err := runner.Run(context.Background(), "facts"); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(captured.args, test.wantArgs) {
				t.Fatalf("args = %#v, want %#v", captured.args, test.wantArgs)
			}
			if !slices.Contains(captured.env, "OPENCODE_CONFIG_CONTENT="+test.wantConfig) {
				t.Fatalf("config override missing from env: %#v", captured.env)
			}
			if captured.stdin != "facts" {
				t.Fatalf("stdin = %q, want facts", captured.stdin)
			}
		})
	}
}

func TestParseResultEnvelopeRejectsIncompleteSynthesis(t *testing.T) {
	data := []byte(`{"type":"result","is_error":false,"result":"{\"goals\":[\"ship\"],\"outcome\":\"done\"}"}`)
	if _, err := parseResultEnvelope(data); err == nil {
		t.Fatal("parseResultEnvelope succeeded without keyDecisions or nextStep")
	}
}

func TestNormalizeKeepsTwelveIntermediateDecisions(t *testing.T) {
	decisions := make([]string, 13)
	for index := range decisions {
		decisions[index] = fmt.Sprintf("decision-%02d", index+1)
	}
	synthesis := session.SessionSynthesis{Goals: []string{"ship"}, KeyDecisions: decisions}

	if err := normalize(&synthesis); err != nil {
		t.Fatal(err)
	}
	if len(synthesis.KeyDecisions) != 12 || synthesis.KeyDecisions[11] != "decision-12" {
		t.Fatalf("key decisions = %#v, want first 12", synthesis.KeyDecisions)
	}
}
