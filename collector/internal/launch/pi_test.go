//go:build !windows

package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func fakePi(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.99.1; exit; fi\nprintf '%s\\n' \"$@\" > \"$CAPTURE\"\n[ -z \"$COSLASH_PI_HANDOFF_FILE\" ] || cp \"$COSLASH_PI_HANDOFF_FILE\" \"$CAPTURE.notes\"\n"
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "agent ' $ ` directory"))
	t.Setenv("COSLASH_HOME", filepath.Join(root, "coslash ' $ ` directory"))
	capture := filepath.Join(root, "capture")
	t.Setenv("CAPTURE", capture)
	return capture
}

func TestPiResumeExactQuotedPath(t *testing.T) {
	capture := fakePi(t)
	path := filepath.Join(t.TempDir(), "custom ' \" $ ` session.jsonl")
	command, _, err := cliCommand(vendors.AgentPi, path, ResumeSession, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, shellQuote("COSLASH_HOME="+os.Getenv("COSLASH_HOME"))) || !strings.Contains(command, shellQuote("PI_CODING_AGENT_DIR="+os.Getenv("PI_CODING_AGENT_DIR"))) {
		t.Fatal("GUI environment overrides missing")
	}
	if output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	args, err := os.ReadFile(capture)
	if err != nil || !strings.HasSuffix(string(args), "--session\n"+path+"\n") {
		t.Fatalf("args=%q err=%v", args, err)
	}
}

func TestPiHandoffPrivateGuardCleanup(t *testing.T) {
	capture := fakePi(t)
	prefix := "quote ' dollar $ backtick `\n"
	notes := prefix + strings.Repeat("x", MaxHandoffBytes-len(prefix))
	command, path, err := cliCommand(vendors.AgentPi, "", NewSession, notes)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("handoff permissions: %v %v", info, err)
	}
	if output, err := exec.Command("/bin/sh", "-c", withCleanup(command, path)).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	got, err := os.ReadFile(capture + ".notes")
	if err != nil || string(got) != handoffPreamble+notes {
		t.Fatalf("notes changed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("not cleaned: %v", err)
	}
	command, path, err = cliCommand(vendors.AgentPi, "", NewSession, "missing notes")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(capture)
	os.Remove(path)
	exec.Command("/bin/sh", "-c", withCleanup(command, path)).Run()
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("Pi ran without handoff")
	}
	if _, _, err := cliCommand(vendors.AgentPi, "", NewSession, strings.Repeat("x", MaxHandoffBytes+1)); err == nil {
		t.Fatal("oversized handoff accepted")
	}
}

func TestPiTranscriptValidationAndLaunchFailure(t *testing.T) {
	fakePi(t)
	cwd := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session","version":3,"id":"custom.id","cwd":` + shellJSON(cwd) + `}`
	if err := os.WriteFile(path, []byte(header), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePiTranscript(path, "custom.id", cwd); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if err := validatePiTranscript(path, "custom.id", cwd); err == nil {
			t.Fatal("unreadable transcript accepted")
		}
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []struct{ path, id, cwd string }{{"relative", "custom.id", cwd}, {path, "different", cwd}, {path, "custom.id", cwd + "missing"}, {cwd, "custom.id", cwd}, {path + "missing", "custom.id", cwd}} {
		if err := validatePiTranscript(change.path, change.id, change.cwd); err == nil {
			t.Fatalf("accepted %+v", change)
		}
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(header, `"version":3`, `"version":4`)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePiTranscript(path, "custom.id", cwd); err == nil {
		t.Fatal("unsupported schema accepted")
	}
	if err := Terminal(context.Background(), "invalid", vendors.AgentPi, cwd, "", NewSession, "notes"); err == nil {
		t.Fatal("terminal error missing")
	}
	entries, err := os.ReadDir(handoffDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("launch failure left handoff: %v %v", entries, err)
	}
	if err := Terminal(context.Background(), "invalid", vendors.AgentPi, cwd+"missing", "", NewSession, ""); err == nil {
		t.Fatal("missing cwd accepted")
	}
	if err := Terminal(context.Background(), "invalid", vendors.AgentPi, path, "", NewSession, ""); err == nil {
		t.Fatal("file cwd accepted")
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := cliCommand(vendors.AgentPi, "", NewSession, ""); err == nil {
		t.Fatal("missing Pi accepted")
	}
	if _, err := remoteCLICommand(vendors.AgentPi, "", NewSession, ""); err == nil {
		t.Fatal("remote Pi accepted")
	}
}

func shellJSON(value string) string { return strconv.Quote(value) }

func TestPiUnsupportedVersionAndUnmanagedExtension(t *testing.T) {
	fakePi(t)
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 0.99.3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cliCommand(vendors.AgentPi, "", NewSession, "notes"); !errors.Is(err, ErrPiUnsupportedVersion) {
		t.Fatalf("unsupported version: %v", err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 0.99.1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "extensions", "coslash-extension.ts")
	os.MkdirAll(filepath.Dir(target), 0700)
	os.WriteFile(target, []byte("user-owned"), 0600)
	if _, _, err := cliCommand(vendors.AgentPi, "", NewSession, "notes"); !errors.Is(err, ErrPiExtension) {
		t.Fatalf("unmanaged extension: %v", err)
	}
	contents, _ := os.ReadFile(target)
	if string(contents) != "user-owned" {
		t.Fatal("user extension overwritten")
	}
}

func TestPiHandoffCleanupAfterCommandFailure(t *testing.T) {
	fakePi(t)
	command, path, err := cliCommand(vendors.AgentPi, "", NewSession, "failure notes")
	if err != nil {
		t.Fatal(err)
	}
	binary, _ := exec.LookPath("pi")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	err = exec.Command("/bin/sh", "-c", withCleanup(command, path)).Run()
	if failure, ok := err.(*exec.ExitError); !ok || failure.ExitCode() != 7 {
		t.Fatalf("CLI failure status was lost: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failure left handoff: %v", err)
	}
}

func TestPiVerifiedReleaseAllowlist(t *testing.T) {
	fakePi(t)
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(binary))
	if err := os.WriteFile(filepath.Join(filepath.Dir(binary), "expect"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"0.99.1", "0.99.2", "0.99.3", "1.0.0", "0.98.0"} {
		if err := os.WriteFile(binary, []byte("#!/bin/sh\necho "+version+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		_, _, err := cliCommand(vendors.AgentPi, "", NewSession, "")
		verified := version == "0.99.1" || version == "0.99.2"
		if available := PiAvailable(); available != verified {
			t.Fatalf("PiAvailable(%s) = %v, want %v", version, available, verified)
		}
		for _, target := range HandoffTargetOptions(context.Background()) {
			if target.Agent == vendors.AgentPi && target.Available != verified {
				t.Fatalf("Pi target availability for %s = %v, want %v", version, target.Available, verified)
			}
		}
		if verified && err != nil {
			t.Fatalf("verified %s rejected: %v", version, err)
		}
		if !verified && !errors.Is(err, ErrPiUnsupportedVersion) {
			t.Fatalf("unverified %s accepted: %v", version, err)
		}
	}
}

func TestPiHandoffCleanupOnProcessGroupSignal(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		for _, signal := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
			t.Run(shell+"/"+signal.String(), func(t *testing.T) {
				if _, err := exec.LookPath(shell); err != nil {
					t.Skipf("%s is unavailable: %v", shell, err)
				}
				capture := fakePi(t)
				binary, _ := exec.LookPath("pi")
				os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.99.1; exit; fi\ntouch \"$CAPTURE\"\nsleep 30\n"), 0700)
				command, path, err := cliCommand(vendors.AgentPi, "", NewSession, "signal cleanup notes")
				if err != nil {
					t.Fatal(err)
				}
				process := exec.Command(shell, "-c", withCleanup(command, path))
				process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := process.Start(); err != nil {
					t.Fatal(err)
				}
				defer syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(capture); err == nil {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				if _, err := os.Stat(capture); err != nil {
					t.Fatal("Pi did not start")
				}
				if err := syscall.Kill(-process.Process.Pid, signal); err != nil {
					t.Fatal(err)
				}
				process.Wait()
				deadline = time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(path); os.IsNotExist(err) {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatal("process-group signal left handoff file")
			})
		}
	}
}

func TestPiCleanupPreservesParentShellTrap(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s is unavailable: %v", shell, err)
			}
			capture := fakePi(t)
			command, path, err := cliCommand(vendors.AgentPi, "", NewSession, "scoped traps")
			if err != nil {
				t.Fatal(err)
			}
			command = "trap " + shellQuote("printf parent > "+shellQuote(capture+".trap")) + " EXIT; " + withCleanup(command, path)
			if output, err := exec.Command(shell, "-c", command).CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, output)
			}
			if got, _ := os.ReadFile(capture + ".trap"); string(got) != "parent" {
				t.Fatal("parent EXIT trap replaced")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("success did not clean")
			}
		})
	}
}

func TestHandoffDirectoryChangeCleanupAndShellState(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s is unavailable: %v", shell, err)
			}
			t.Setenv("SHELL", shell)
			capture := fakePi(t)
			original := localTerminalOpener
			t.Cleanup(func() { localTerminalOpener = original })
			for _, failure := range []string{"none", "missing", "inaccessible"} {
				if failure == "inaccessible" && os.Geteuid() == 0 {
					continue
				}
				missing := failure != "none"
				cwd := t.TempDir()
				t.Cleanup(func() { _ = os.Chmod(cwd, 0700) })
				localTerminalOpener = func(_ context.Context, _, _, directory, command string) error {
					if failure == "inaccessible" {
						if err := os.Chmod(cwd, 0); err != nil {
							return err
						}
					}
					if failure == "missing" {
						if err := os.Remove(cwd); err != nil {
							return err
						}
					}
					script := terminalScript(shell, directory, command) + "; result=$?; pwd > " + shellQuote(capture+".cwd") + "; exit \"$result\""
					return exec.Command(shell, "-c", script).Run()
				}
				os.Remove(capture)
				err := Terminal(context.Background(), "terminal", vendors.AgentPi, cwd, "", NewSession, "private notes")
				if missing {
					if err == nil {
						t.Fatal("failed cd succeeded")
					}
					if _, err := os.Stat(capture); !os.IsNotExist(err) {
						t.Fatal("agent ran after failed cd")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					got, _ := os.ReadFile(capture + ".cwd")
					if strings.TrimSpace(string(got)) != cwd {
						t.Fatalf("cwd lost: %q", got)
					}
				}
				entries, err := os.ReadDir(handoffDir())
				if err != nil || len(entries) != 0 {
					t.Fatalf("private notes remain: %v %v", entries, err)
				}
			}
		})
	}
}

func TestPiPromptUsesPrivateTransportAndCleansContext(t *testing.T) {
	capture := fakePi(t)
	fakeExpect := filepath.Join(filepath.Dir(mustLookPathForPi(t, "pi")), "expect")
	script := "#!/bin/sh\ncp \"$COSLASH_PROMPT_PATH\" \"$CAPTURE.prompt\"\nprintf '%s\\n' \"$COSLASH_PI_READY\" > \"$CAPTURE.ready\"\nprintf '%s\\n' \"$@\" > \"$CAPTURE.relay\"\nrm -f \"$COSLASH_PROMPT_PATH\"\n/bin/sh -c \"$COSLASH_BASE\"\n"
	if err := os.WriteFile(fakeExpect, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	command, path, err := cliCommandWithPrompt(vendors.AgentPi, "", NewSession, "private background", "private request")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(command, "private request") || strings.Contains(command, "private background") {
		t.Fatal("private contents leaked into terminal command")
	}
	if _, err := os.Stat(path + ".context"); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/sh", "-c", localHandoffScript(t.TempDir(), command, path)).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	ready, _ := os.ReadFile(capture + ".ready")
	if strings.TrimSpace(string(ready)) != filepath.Base(path) {
		t.Fatal("readiness nonce not forwarded to Pi")
	}
	relay, _ := os.ReadFile(capture + ".relay")
	if !strings.Contains(string(relay), `\x1b\]777;coslash-ready=`+filepath.Base(path)+`\x07`) || strings.Contains(string(relay), `\x1b\[\?2004h`) {
		t.Fatal("Pi prompt delivery does not wait for managed startup readiness")
	}
	prompt, _ := os.ReadFile(capture + ".prompt")
	if !strings.Contains(string(prompt), "private request") {
		t.Fatal("prompt not delivered")
	}
	notes, _ := os.ReadFile(capture + ".notes")
	if string(notes) != handoffPreamble+"private background" {
		t.Fatal("handoff not delivered")
	}
	for _, file := range []string{path, path + ".context"} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("staged file remains %s", file)
		}
	}
	command, path, err = cliCommandWithPrompt(vendors.AgentPi, "", NewSession, "private background", "private request")
	if err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("/bin/sh", "-c", localHandoffScript(filepath.Join(t.TempDir(), "missing"), command, path)).Run()
	for _, file := range []string{path, path + ".context"} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("failed cd leaves file %s", file)
		}
	}
	command, path, err = cliCommandWithPrompt(vendors.AgentPi, "", NewSession, "private background", "private request")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fakeExpect, []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	err = exec.Command("/bin/sh", "-c", localHandoffScript(t.TempDir(), command, path)).Run()
	if failure, ok := err.(*exec.ExitError); !ok || failure.ExitCode() != 7 {
		t.Fatalf("startup failure status was lost: %v", err)
	}
	for _, file := range []string{path, path + ".context"} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("startup failure leaves file %s", file)
		}
	}
}
func mustLookPathForPi(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStagedLaunchFishUsesNativeParentAndPOSIXChild(t *testing.T) {
	fakePi(t)
	t.Setenv("SHELL", "/usr/local/bin/fish")
	for _, agent := range []string{vendors.AgentPi, vendors.AgentClaude, vendors.AgentCodex, vendors.AgentOpenCode} {
		command, path, err := cliCommand(agent, "", NewSession, "private notes")
		if err != nil {
			t.Fatal(err)
		}
		script := terminalScript("/usr/local/bin/fish", ".", localHandoffScript("/project ' quoted", command, path))
		if !strings.Contains(script, "set --erase ") || !strings.Contains(script, "; else; ") || !strings.HasSuffix(script, "; end") || strings.Contains(script, "; then ") || strings.Contains(script, "; fi") {
			t.Fatalf("invalid parent fish script: %s", script)
		}
		if !strings.Contains(script, shellJoin("/bin/sh", "-c")) {
			t.Fatal("cleanup/CLI not isolated in explicit POSIX child")
		}
		_ = removeHandoffFile(path)
	}
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Log("fish is not installed; validated native parent dispatch and explicit POSIX child construction for all staged providers")
		return
	}
	for _, missing := range []bool{false, true} {
		cwd := t.TempDir()
		command, path, err := cliCommand(vendors.AgentPi, "", NewSession, "private notes")
		if err != nil {
			t.Fatal(err)
		}
		if missing {
			os.Remove(cwd)
		}
		script := terminalScript(fish, ".", localHandoffScript(cwd, command, path)) + `; set -l result $status; pwd; /bin/sh -c 'exit "$1"' sh "$result"`
		output, err := exec.Command(fish, "-c", script).CombinedOutput()
		if missing && err == nil {
			t.Fatal("missing cd succeeded")
		}
		if !missing && (err != nil || !strings.Contains(string(output), cwd)) {
			t.Fatalf("fish cwd/launch %s %v", output, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("fish left notes")
		}
	}
}
func TestPiForwardedDirectoriesStayRelativeToCollector(t *testing.T) {
	capture := fakePi(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	state := filepath.Join(private, "relative state")
	agent := filepath.Join(private, "relative agent")
	sessions := filepath.Join(private, "relative sessions")
	for _, test := range []struct{ name, path string }{{"COSLASH_HOME", state}, {"PI_CODING_AGENT_DIR", agent}, {"PI_CODING_AGENT_SESSION_DIR", sessions}} {
		relative, err := filepath.Rel(cwd, test.path)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(test.name, relative)
	}
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.99.2;exit;fi\nprintf '%s\\n' \"$COSLASH_HOME\" \"$PI_CODING_AGENT_DIR\" \"$PI_CODING_AGENT_SESSION_DIR\" > \"$CAPTURE.env\"\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, sessionRoot := range []string{os.Getenv("PI_CODING_AGENT_SESSION_DIR"), "~/pi override with spaces"} {
		t.Setenv("PI_CODING_AGENT_SESSION_DIR", sessionRoot)
		command, _, err := cliCommand(vendors.AgentPi, "", NewSession, "")
		if err != nil {
			t.Fatal(err)
		}
		project := t.TempDir()
		process := exec.Command("/bin/sh", "-c", command)
		process.Dir = project
		if out, err := process.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		expectedSession := sessions
		if strings.HasPrefix(sessionRoot, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			expectedSession = filepath.Join(home, strings.TrimPrefix(sessionRoot, "~/"))
		}
		got, err := os.ReadFile(capture + ".env")
		if err != nil || string(got) != state+"\n"+agent+"\n"+expectedSession+"\n" {
			t.Fatalf("forwarded paths %q %v", got, err)
		}
	}
}
