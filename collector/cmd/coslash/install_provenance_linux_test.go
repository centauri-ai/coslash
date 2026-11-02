//go:build linux

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCustomScriptRegistrationEnablesUpdateTarget(t *testing.T) {
	if expected := os.Getenv("COSLASH_TEST_CUSTOM_CHANNEL"); expected != "" {
		releaseBuildMetadata, branchBuildMetadata = "true", "false"
		if got := detectedInstallChannel(); got != expected {
			executable, _ := os.Executable()
			home, _ := os.UserHomeDir()
			t.Fatalf("channel=%q, want %q (executable=%q home=%q trusted=%t)", got, expected, executable, home, trustedCustomScriptInstall(executable, home))
		}
		_, err := updateTarget()
		if (err == nil) != (expected == "script") {
			t.Fatalf("update target error=%v, channel=%q", err, expected)
		}
		return
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "custom")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(current)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := filepath.Join(dir, "coslash")
	destination, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	run := func(channel string) {
		t.Helper()
		command := exec.Command(target, "-test.run=^TestCustomScriptRegistrationEnablesUpdateTarget$")
		command.Env = append(os.Environ(), "HOME="+home, "COSLASH_TEST_CUSTOM_CHANNEL="+channel)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("channel %s: %v\n%s", channel, err, out)
		}
	}
	run("unknown")
	if err := os.WriteFile(filepath.Join(dir, scriptInstallMarker), []byte(scriptInstallMarkerContents), 0o600); err != nil {
		t.Fatal(err)
	}
	run("script")
}
