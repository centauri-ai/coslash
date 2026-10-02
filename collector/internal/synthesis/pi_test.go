package synthesis

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestPiSynthesis(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, model := range []string{"default", "amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0"} {
		t.Run(model, func(t *testing.T) {
			runner := &CLIRunner{Backend: settings.BackendPi, Bin: "pi", Model: model, Timeout: time.Second}
			if runner.VendorName() != "pi" {
				t.Fatal("Pi vendor identity missing")
			}
			runner.exec = func(_ context.Context, spec commandSpec) ([]byte, error) {
				want := []string{"--print", "--no-session", "--no-tools", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--system-prompt", systemPrompt + jsonInstruction, "--append-system-prompt", ""}
				if model != "default" {
					want = append(want, "--model", model)
				}
				if spec.bin != "pi" || !slices.Equal(spec.args, want) {
					t.Fatalf("command=%#v", spec)
				}
				if spec.stdin != "private session facts" || strings.Contains(strings.Join(spec.args, " "), spec.stdin) {
					t.Fatal("session facts must be stdin only")
				}
				if len(spec.env) != 0 {
					t.Fatal("must inherit Pi configuration and provider/SSO environment")
				}
				if filepath.Dir(spec.dir) != SynthesisCwd() || !strings.HasPrefix(filepath.Base(spec.dir), ".pi-") {
					t.Fatalf("scratch=%s", spec.dir)
				}
				if _, err := os.Stat(spec.dir); err != nil {
					t.Fatal(err)
				}
				return []byte("```json\n{\"goals\":[\"Ship Pi synthesis\"],\"outcome\":\"Backend added\",\"keyDecisions\":[],\"nextStep\":\"Review\"}\n```"), nil
			}
			got, err := runner.Run(context.Background(), "private session facts")
			if err != nil || got.Synthesis.Outcome != "Backend added" {
				t.Fatalf("got=%#v err=%v", got, err)
			}
			if got.Usage.Coverage != "unknown" || got.Usage.ReportedCostMicroUSD != nil || got.Usage.EstimatedCostMicroUSD != nil {
				t.Fatalf("Pi usage must remain unknown: %#v", got.Usage)
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

func TestPiSynthesisFailure(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	for _, tc := range []struct {
		name, output string
		err          error
		want         string
	}{
		{"missing fields", `{"outcome":"looks valid"}`, nil, "synthesis result"},
		{"provider auth", "private-token", &exec.ExitError{}, "verify CLI authentication and the selected model"},
		{"missing CLI", "", exec.ErrNotFound, "not installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &CLIRunner{Backend: "pi-cli", Bin: "pi", Model: "default", Timeout: time.Second, exec: func(context.Context, commandSpec) ([]byte, error) { return []byte(tc.output), tc.err }}
			_, err := r.Run(context.Background(), "facts")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("error=%v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &CLIRunner{Backend: "pi-cli", Bin: "pi", Model: "default", Timeout: time.Second, exec: func(ctx context.Context, _ commandSpec) ([]byte, error) { return nil, ctx.Err() }}
	if _, err := r.Run(ctx, "facts"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestPiRunnerPlatformAvailability(t *testing.T) {
	runner, err := NewRunner(settings.SynthesisSettings{Enabled: true, Backend: settings.BackendPi, Model: settings.PiDefaultModel})
	if vendors.PiSupported() {
		if err != nil || runner == nil {
			t.Fatalf("supported Pi runner unavailable: %v", err)
		}
	} else if err == nil || runner != nil || !strings.Contains(err.Error(), "macOS") {
		t.Fatalf("unsupported Pi runner = %v, %v", runner, err)
	}
}
