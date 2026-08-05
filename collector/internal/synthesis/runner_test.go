package synthesis

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

// Run reads Bin, Model, and Timeout straight off the struct with no fallbacks,
// so NewRunner is the only place that can set them.
func TestNewRunner(t *testing.T) {
	for _, testCase := range []struct {
		config  settings.SynthesisSettings
		bin     string
		wantNil bool
		wantErr bool
	}{
		{config: settings.SynthesisSettings{Enabled: false, Backend: settings.BackendClaude}, wantNil: true},
		{config: settings.SynthesisSettings{Enabled: true, Backend: settings.BackendClaude, Model: "claude-haiku-4-5"}, bin: "claude"},
		{config: settings.SynthesisSettings{Enabled: true, Backend: settings.BackendCodex, Model: "gpt-5.6-luna"}, bin: "codex"},
		{config: settings.SynthesisSettings{Enabled: true, Backend: "nope"}, wantErr: true},
	} {
		runner, err := NewRunner(testCase.config)
		if (err != nil) != testCase.wantErr {
			t.Fatalf("%+v: err = %v, wantErr %v", testCase.config, err, testCase.wantErr)
		}
		if testCase.wantErr {
			continue
		}
		if testCase.wantNil {
			if runner != nil {
				t.Fatalf("%+v: want nil runner when synthesis is disabled", testCase.config)
			}
			continue
		}
		cli, ok := runner.(*CLIRunner)
		if !ok {
			t.Fatalf("%+v: want *CLIRunner, got %T", testCase.config, runner)
		}
		if cli.Bin != testCase.bin {
			t.Errorf("%+v: Bin = %q, want %q", testCase.config, cli.Bin, testCase.bin)
		}
		if cli.Model != testCase.config.Model {
			t.Errorf("%+v: Model = %q, want %q", testCase.config, cli.Model, testCase.config.Model)
		}
		if cli.Timeout <= 0 {
			t.Errorf("%+v: Timeout = %v, want a positive timeout", testCase.config, cli.Timeout)
		}
	}
}
