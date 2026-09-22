//go:build windows

package diagnostics

import (
	"context"
	"reflect"
	"testing"
)

func TestVersionCommandUsesWindowsPowerShellForScripts(t *testing.T) {
	bin := `C:\Users\person\AppData\Local\agent\agent.ps1`
	command := versionCommand(context.Background(), bin)
	want := []string{"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", bin, "--version"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("arguments = %#v, want %#v", command.Args, want)
	}
}

func TestVersionCommandExecutesNativeBinaryDirectly(t *testing.T) {
	bin := `C:\Tools\agent.exe`
	command := versionCommand(context.Background(), bin)
	want := []string{bin, "--version"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("arguments = %#v, want %#v", command.Args, want)
	}
}
