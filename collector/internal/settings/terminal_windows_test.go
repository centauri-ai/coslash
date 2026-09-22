package settings

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodeMigratesLegacyWindowsTerminal(t *testing.T) {
	data := strings.ReplaceAll(`{
  "$schema": "SCHEMA",
  "version": 1,
  "synthesis": {"enabled": false, "backend": "claude-cli", "model": "claude-haiku-4-5"},
  "appearance": {"theme": "light"},
  "launch": {"terminal": "terminal"}
}`, "SCHEMA", SchemaURL)
	config, err := Decode([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if config.Launch.Terminal != TerminalWindows {
		t.Fatalf("terminal = %q, want %q", config.Launch.Terminal, TerminalWindows)
	}
}

func TestWindowsTerminalSettings(t *testing.T) {
	if got := Defaults().Launch.Terminal; got != TerminalWindows {
		t.Fatalf("default terminal = %q, want %q", got, TerminalWindows)
	}
	want := []TerminalOption{{ID: TerminalWindows, Label: "Windows Terminal"}}
	if got := TerminalOptions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalOptions() = %#v, want %#v", got, want)
	}
}
