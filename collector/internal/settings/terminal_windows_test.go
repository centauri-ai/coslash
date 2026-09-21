package settings

import (
	"reflect"
	"testing"
)

func TestWindowsTerminalSettings(t *testing.T) {
	if got := Defaults().Launch.Terminal; got != TerminalWindows {
		t.Fatalf("default terminal = %q, want %q", got, TerminalWindows)
	}
	want := []TerminalOption{{ID: TerminalWindows, Label: "Windows Terminal"}}
	if got := TerminalOptions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalOptions() = %#v, want %#v", got, want)
	}
}
