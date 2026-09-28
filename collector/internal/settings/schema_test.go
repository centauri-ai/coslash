package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestSettingsSchemaAcceptsWindowsTerminal(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Launch struct {
				Properties struct {
					Terminal struct {
						Enum []string `json:"enum"`
					} `json:"terminal"`
				} `json:"properties"`
			} `json:"launch"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	for _, terminal := range schema.Properties.Launch.Properties.Terminal.Enum {
		if terminal == TerminalWindows {
			return
		}
	}
	t.Fatalf("launch.terminal enum = %q, want %q", schema.Properties.Launch.Properties.Terminal.Enum, TerminalWindows)
}

func TestPiSynthesisSettings(t *testing.T) {
	config := Defaults()
	config.Synthesis = SynthesisSettings{Enabled: true, Backend: BackendPi, Model: PiDefaultModel}
	for _, model := range []string{PiDefaultModel, "amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0"} {
		config.Synthesis.Model = model
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(data)
		if err != nil || got.Synthesis != config.Synthesis {
			t.Fatalf("got=%#v err=%v", got, err)
		}
	}
	wantExecutable := ""
	if vendors.PiSupported() {
		wantExecutable = "pi"
	}
	if BackendExecutable(BackendPi) != wantExecutable {
		t.Fatal("Pi backend availability does not match platform support")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Synthesis struct {
				Properties struct {
					Backend struct {
						Enum []string `json:"enum"`
					} `json:"backend"`
				} `json:"properties"`
			} `json:"synthesis"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(schema.Properties.Synthesis.Properties.Backend.Enum, BackendPi) {
		t.Fatal("settings schema rejects Pi")
	}
}

func TestLocalSyncPauseRoundTripsWithoutChangingLegacyDefaults(t *testing.T) {
	config := Defaults()
	if config.SyncPaused {
		t.Fatal("legacy default unexpectedly pauses sync")
	}
	config.SyncPaused = true
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil || !decoded.SyncPaused {
		t.Fatalf("decoded pause=%v err=%v", decoded.SyncPaused, err)
	}
}
