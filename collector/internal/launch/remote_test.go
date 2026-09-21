package launch

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

const testRemoteHandoffName = "0123456789abcdef0123456789abcdef"

func TestRemoteHandoffContentsPreserveBoundaryPayload(t *testing.T) {
	special := []byte("🦖\n'\"\\$();&|<>\n")
	payload := append(special, bytes.Repeat([]byte("<"), MaxHandoffBytes-len(special))...)
	contents, err := RemoteHandoffContents(vendors.AgentClaude, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(handoffPreamble), payload...)
	if !bytes.Equal(contents, want) {
		t.Fatalf("contents changed: got %d bytes, want %d", len(contents), len(want))
	}
}

func TestRemoteCLICommandUsesStagedHandoffName(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentClaude, "", NewSession, testRemoteHandoffName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, `handoff="$HOME"/'.coslash/handoffs/`+testRemoteHandoffName+`'`) ||
		!strings.Contains(command, "'--append-system-prompt-file' \"$handoff\"") ||
		!strings.Contains(command, `trap 'rm -f "$handoff"' EXIT HUP INT TERM`) {
		t.Fatalf("command = %q", command)
	}
	if strings.Contains(command, "base64") || len(command) > 1024 {
		t.Fatalf("command carries handoff data: %d bytes: %q", len(command), command)
	}
}

func TestRemoteCodexCLICommandLoadsBoundaryHandoffWithoutExpandingArguments(t *testing.T) {
	requirePOSIXRemoteShell(t)
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	handoffDir := filepath.Join(home, ".coslash", "handoffs")
	bin := filepath.Join(home, "bin")
	for _, dir := range []string{codexHome, handoffDir, bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	contents, err := RemoteHandoffContents(vendors.AgentCodex, strings.Repeat("<", MaxHandoffBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handoffDir, testRemoteHandoffName), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	fakeCodex := filepath.Join(bin, "codex")
	if err := os.WriteFile(fakeCodex, []byte(`#!/bin/sh
[ "$1" = --profile ] || exit 90
cp "$CODEX_HOME/$2.config.toml" "$HOME/captured-profile"
printf '%s\n' "$@" > "$HOME/codex-args"
`), 0o700); err != nil {
		t.Fatal(err)
	}

	command, err := remoteCLICommand(vendors.AgentCodex, "", NewSession, testRemoteHandoffName)
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command("/bin/sh", "-c", command)
	process.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+codexHome, "PATH="+bin+":"+os.Getenv("PATH"))
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("command failed: %v: %s", err, output)
	}
	profile, err := os.ReadFile(filepath.Join(home, "captured-profile"))
	if err != nil {
		t.Fatal(err)
	}
	wantProfile := append([]byte("developer_instructions = "), contents...)
	wantProfile = append(wantProfile, '\n')
	if !bytes.Equal(profile, wantProfile) {
		t.Fatalf("profile changed handoff: got %d bytes, want %d", len(profile), len(wantProfile))
	}
	profileInfo, err := os.Stat(filepath.Join(home, "captured-profile"))
	if err != nil {
		t.Fatal(err)
	}
	if profileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("profile mode = %o, want 600", profileInfo.Mode().Perm())
	}
	args, err := os.ReadFile(filepath.Join(home, "codex-args"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "--profile\ncoslash-" + testRemoteHandoffName + "\n"; string(args) != want {
		t.Fatalf("codex arguments = %q, want %q", args, want)
	}
}

func TestRemoteCodexCLICommandStopsWhenHandoffReadFails(t *testing.T) {
	requirePOSIXRemoteShell(t)
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	bin := filepath.Join(home, "bin")
	for _, dir := range []string{codexHome, bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fakeCodex := filepath.Join(bin, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\ntouch \"$HOME/codex-launched\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	command, err := remoteCLICommand(vendors.AgentCodex, "", NewSession, testRemoteHandoffName)
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command("/bin/sh", "-c", command)
	process.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+codexHome, "PATH="+bin+":"+os.Getenv("PATH"))
	if err := process.Run(); err == nil {
		t.Fatal("command succeeded without its staged handoff")
	}
	if _, err := os.Stat(filepath.Join(home, "codex-launched")); !os.IsNotExist(err) {
		t.Fatalf("Codex launched after handoff read failed: %v", err)
	}
}

func TestRemoteCodexCLICommandCreatesMissingProfileDirectory(t *testing.T) {
	requirePOSIXRemoteShell(t)
	home := t.TempDir()
	codexHome := filepath.Join(home, "new-codex-home")
	handoffDir := filepath.Join(home, ".coslash", "handoffs")
	bin := filepath.Join(home, "bin")
	for _, dir := range []string{handoffDir, bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(handoffDir, testRemoteHandoffName), []byte("private handoff"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(
		"#!/bin/sh\n[ -f \"$CODEX_HOME/$2.config.toml\" ] || exit 91\ntouch \"$HOME/codex-launched\"\n",
	), 0o700); err != nil {
		t.Fatal(err)
	}

	command, err := remoteCLICommand(vendors.AgentCodex, "", NewSession, testRemoteHandoffName)
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command("/bin/sh", "-c", command)
	process.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+codexHome, "PATH="+bin+":"+os.Getenv("PATH"))
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("command failed: %v: %s", err, output)
	}
	info, err := os.Stat(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("Codex profile directory mode = %o, want 700", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(home, "codex-launched")); err != nil {
		t.Fatalf("Codex did not launch: %v", err)
	}
}

func TestRemoteCLICommandRejectsInvalidHandoffName(t *testing.T) {
	if _, err := remoteCLICommand(vendors.AgentCodex, "", NewSession, "../../handoff"); err == nil {
		t.Fatal("remoteCLICommand accepted an invalid handoff name")
	}
}

func TestRemoteTerminalCommandRemovesHandoffWhenWorkingDirectoryIsMissing(t *testing.T) {
	requirePOSIXRemoteShell(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".coslash", "handoffs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, testRemoteHandoffName)
	if err := os.WriteFile(path, []byte("private handoff"), 0o600); err != nil {
		t.Fatal(err)
	}
	command, err := remoteTerminalCommand(
		vendors.AgentCodex, filepath.Join(home, "missing"), "", NewSession, testRemoteHandoffName,
	)
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command("/bin/sh", "-c", command)
	process.Env = append(os.Environ(), "HOME="+home)
	if err := process.Run(); err == nil {
		t.Fatal("remote command succeeded with a missing working directory")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("staged handoff still exists: %v", err)
	}
}

func requirePOSIXRemoteShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("executes the POSIX command on the Linux SSH peer")
	}
}

func TestRemoteCLICommandResumesValidatedSession(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentCodex, "01234567-89ab-cdef-0123-456789abcdef", ResumeSession, "")
	if err != nil {
		t.Fatal(err)
	}
	if command != "'codex' 'resume' '01234567-89ab-cdef-0123-456789abcdef'" {
		t.Fatalf("command = %q", command)
	}
}

func TestCursorCLICommandResumesValidatedSession(t *testing.T) {
	command, _, err := cliCommand(vendors.AgentCursor, "01234567-89ab-cdef-0123-456789abcdef", ResumeSession, "")
	if err != nil {
		t.Fatal(err)
	}
	want := localCommandJoin(localCLIExecutable(vendors.AgentCursor, "agent"), "--resume", "01234567-89ab-cdef-0123-456789abcdef")
	if command != want {
		t.Fatalf("command = %q", command)
	}
}

func TestCursorFreshSessionLeavesHandoffForClipboard(t *testing.T) {
	command, path, err := cliCommand(vendors.AgentCursor, "", NewSession, "handoff")
	if err != nil {
		t.Fatal(err)
	}
	want := localCommandJoin(localCLIExecutable(vendors.AgentCursor, "agent"))
	if command != want || path != "" {
		t.Fatalf("command = %q, path = %q", command, path)
	}
}

func TestValidWorkingDirectory(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !ValidWorkingDirectory(directory) {
		t.Fatalf("ValidWorkingDirectory(%q) = false", directory)
	}
	for _, path := range []string{"", filepath.Join(directory, "missing"), file} {
		if ValidWorkingDirectory(path) {
			t.Fatalf("ValidWorkingDirectory(%q) = true", path)
		}
	}
}
