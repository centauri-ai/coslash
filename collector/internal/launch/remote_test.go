package launch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestRemoteCLICommandUsesRemoteHandoffFile(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentClaude, "", "", NewSession, "keep this private")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "mktemp") || !strings.Contains(command, "base64 -d") ||
		!strings.Contains(command, "'--append-system-prompt-file' \"$handoff\"") {
		t.Fatalf("command = %q", command)
	}
	if strings.Contains(command, "keep this private") {
		t.Fatalf("command contains the raw handoff: %q", command)
	}
}

func TestRemoteCodexCLICommandReadsRemoteHandoffFile(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentCodex, "", "", NewSession, "keep this private")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, `developer_instructions=$(cat "$handoff")`) || strings.Contains(command, `cat \"$handoff\"`) {
		t.Fatalf("command = %q", command)
	}
}

func TestRemoteCLICommandResumesValidatedSession(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentCodex, "", "01234567-89ab-cdef-0123-456789abcdef", ResumeSession, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, `"$coslash_agent" 'resume' '01234567-89ab-cdef-0123-456789abcdef'`) {
		t.Fatalf("command = %q", command)
	}
}

func TestRemoteOpenCodeCLICommandPreservesResumeAndHandoffModes(t *testing.T) {
	resume, err := remoteCLICommand(vendors.AgentOpenCode, "", "ses_abc123", ResumeSession, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resume, `"$coslash_agent" '--session' 'ses_abc123'`) {
		t.Fatalf("resume command = %q", resume)
	}

	handoff, err := remoteCLICommand(vendors.AgentOpenCode, "", "", NewSession, "keep this private")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(handoff, "mktemp") || !strings.Contains(handoff, "base64 -d") ||
		!strings.Contains(handoff, `OPENCODE_CONFIG_CONTENT='{"instructions":["'"$handoff"'"]}' "$coslash_agent"`) {
		t.Fatalf("handoff command = %q", handoff)
	}
	if strings.Contains(handoff, "keep this private") {
		t.Fatalf("handoff command contains raw handoff: %q", handoff)
	}
}

func TestRemoteCLICommandKeepsRootLevelExecutableInRoot(t *testing.T) {
	command, err := remoteCLICommand(vendors.AgentCodex, "/codex", "", NewSession, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "coslash_dir=${coslash_candidate%/*}\n  coslash_base=${coslash_candidate##*/}\n  [ -n \"$coslash_dir\" ] || coslash_dir=/") {
		t.Fatalf("root-level executable normalization is missing: %q", command)
	}
}

func TestRemoteCLICommandResolvesExecutablesInTheRemoteShell(t *testing.T) {
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex, vendors.AgentOpenCode} {
		t.Run(agent, func(t *testing.T) {
			home := t.TempDir()
			fallback := filepath.Join(home, ".toolbox", "bin", agent)
			writeRemoteExecutable(t, fallback)
			command, err := remoteCLICommand(agent, "", "", NewSession, "")
			if err != nil {
				t.Fatal(err)
			}
			got, err := runRemoteCommand(t, command, home, "", t.TempDir())
			if err != nil {
				t.Fatalf("run resolver: %v\ncommand: %s", err, command)
			}
			want := physicalPath(t, fallback) + "\n"
			if got != want {
				t.Fatalf("resolved executable = %q, want %q", got, want)
			}
		})
	}
}

func TestRemoteCLICommandResolutionPrecedenceAndFailures(t *testing.T) {
	home := t.TempDir()
	pathDir := t.TempDir()
	pathExecutable := filepath.Join(pathDir, "codex")
	fallback := filepath.Join(home, ".toolbox", "bin", "codex")
	override := filepath.Join(home, "custom tools", "codex;still-data")
	writeRemoteExecutable(t, pathExecutable)
	writeRemoteExecutable(t, fallback)
	writeRemoteExecutable(t, override)

	for _, test := range []struct {
		name      string
		override  string
		path      string
		want      string
		wantFail  bool
		emptyHome bool
	}{
		{name: "override wins", override: override, path: pathDir, want: override},
		{name: "path wins over fallback", path: pathDir, want: pathExecutable},
		{name: "home relative override", override: "~/custom tools/codex;still-data", path: pathDir, want: override},
		{name: "invalid override does not fall back", override: filepath.Join(home, "missing"), path: pathDir, wantFail: true},
		{name: "missing executable", path: "", wantFail: true, emptyHome: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, err := remoteCLICommand(vendors.AgentCodex, test.override, "", NewSession, "")
			if err != nil {
				t.Fatal(err)
			}
			runHome := home
			if test.emptyHome {
				runHome = t.TempDir()
			}
			got, runErr := runRemoteCommand(t, command, runHome, test.path, t.TempDir())
			if test.wantFail {
				if runErr == nil || !strings.Contains(got, "coSlash: could not find an executable for codex") {
					t.Fatalf("failure = %v, output = %q", runErr, got)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("run resolver: %v\ncommand: %s", runErr, command)
			}
			want := physicalPath(t, test.want) + "\n"
			if got != want {
				t.Fatalf("resolved executable = %q, want %q", got, want)
			}
		})
	}
}

func TestRemoteCLICommandNormalizesRelativePathAndAvoidsShellSideEffects(t *testing.T) {
	home := t.TempDir()
	workingDirectory := t.TempDir()
	relativeExecutable := filepath.Join(workingDirectory, "bin", "claude")
	writeRemoteExecutable(t, relativeExecutable)
	command, err := remoteCLICommand(vendors.AgentClaude, "", "", NewSession, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(command, "eval") || strings.Contains(command, "source") || strings.Contains(command, "PATH=") || strings.Contains(command, "-l") {
		t.Fatalf("resolver has forbidden shell behavior: %s", command)
	}
	got, err := runRemoteCommand(t, command, home, "bin", workingDirectory)
	if err != nil {
		t.Fatalf("run resolver: %v\ncommand: %s", err, command)
	}
	want := physicalPath(t, relativeExecutable) + "\n"
	if got != want {
		t.Fatalf("resolved executable = %q, want %q", got, want)
	}
}

func writeRemoteExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$0\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func physicalPath(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

func runRemoteCommand(t *testing.T, command, home, path, workingDirectory string) (string, error) {
	t.Helper()
	process := exec.Command("/bin/sh", "-c", command)
	process.Dir = workingDirectory
	process.Env = []string{"HOME=" + home, "PATH=" + path}
	output, err := process.CombinedOutput()
	return string(output), err
}
