package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
