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
