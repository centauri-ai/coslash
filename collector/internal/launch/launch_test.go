package launch

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestRemoteCommandBuildersExactOutput(t *testing.T) {
	session := "9c73be46-52af-4b1d-9ee7-123456789abc"
	handoff := "h_4f16c2d8e25a4ce88ee8d1d02810d455"

	got, err := LaunchResumeRemoteCommand(vendors.AgentCodex, session)
	if err != nil {
		t.Fatal(err)
	}
	want := `exec "$HOME/.local/bin/coslash" launch --agent codex --session 9c73be46-52af-4b1d-9ee7-123456789abc --mode resume`
	if got != want {
		t.Fatalf("resume =\n%q\nwant\n%q", got, want)
	}

	got, err = LaunchNewRemoteCommand(vendors.AgentClaude, "b21e7f04-79bf-46db-b37f-123456789abc", handoff)
	if err != nil {
		t.Fatal(err)
	}
	want = `exec "$HOME/.local/bin/coslash" launch --agent claude --session b21e7f04-79bf-46db-b37f-123456789abc --mode new --handoff h_4f16c2d8e25a4ce88ee8d1d02810d455`
	if got != want {
		t.Fatalf("new =\n%q\nwant\n%q", got, want)
	}

	got, err = HandoffPutRemoteCommand(vendors.AgentClaude, session)
	if err != nil {
		t.Fatal(err)
	}
	want = `exec "$HOME/.local/bin/coslash" handoff put --agent claude --session 9c73be46-52af-4b1d-9ee7-123456789abc`
	if got != want {
		t.Fatalf("handoff =\n%q\nwant\n%q", got, want)
	}
}

func TestRemoteCommandBuildersRejectHostileInput(t *testing.T) {
	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "agent shell",
			call: func() error {
				_, err := LaunchResumeRemoteCommand("claude;rm", "9c73be46-52af-4b1d-9ee7-123456789abc")
				return err
			},
		},
		{
			name: "session path",
			call: func() error {
				_, err := LaunchResumeRemoteCommand(vendors.AgentClaude, "../etc/passwd")
				return err
			},
		},
		{
			name: "handoff injection",
			call: func() error {
				_, err := LaunchNewRemoteCommand(
					vendors.AgentClaude,
					"9c73be46-52af-4b1d-9ee7-123456789abc",
					"h_4f16c2d8e25a4ce88ee8d1d02810d455; reboot",
				)
				return err
			},
		},
		{
			name: "alias injection",
			call: func() error {
				_, err := InteractiveSSHCommand("host;rm", `exec "$HOME/.local/bin/coslash" snapshot --probe`)
				return err
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestInteractiveSSHCommandQuotesRemoteCommand(t *testing.T) {
	remote := `exec "$HOME/.local/bin/coslash" launch --agent claude --session 9c73be46-52af-4b1d-9ee7-123456789abc --mode resume`
	got, err := InteractiveSSHCommand("gpu-server", remote)
	if err != nil {
		t.Fatal(err)
	}
	want := "ssh -t -- gpu-server " + shellQuote(remote)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPutClaimSweepHandoffLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	session := "9c73be46-52af-4b1d-9ee7-123456789abc"
	hostile := "notes with $(reboot) and `id` and ; rm -rf /"

	id, err := PutHandoff(vendors.AgentClaude, session, strings.NewReader(hostile))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateHandoffID(id); err != nil {
		t.Fatal(err)
	}
	path := handoffPath(id)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o", info.Mode().Perm())
	}
	dirInfo, err := os.Lstat(handoffDir())
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o", dirInfo.Mode().Perm())
	}

	body, cleanup, err := ClaimHandoff(vendors.AgentClaude, session, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, handoffPreamble) || !strings.Contains(body, hostile) {
		t.Fatalf("body missing preamble or text")
	}
	if _, _, err := ClaimHandoff(vendors.AgentClaude, session, id); !errors.Is(err, ErrHandoffUsed) {
		t.Fatalf("second claim err = %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(claimedHandoffPath(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("claimed file remained")
	}
}

func TestPutHandoffRejectsInvalidUTF8AndOversize(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	session := "9c73be46-52af-4b1d-9ee7-123456789abc"
	if _, err := PutHandoff(vendors.AgentClaude, session, bytes.NewReader([]byte{0xff, 0xfe})); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid utf8 err = %v", err)
	}
	if entries, _ := os.ReadDir(handoffDir()); len(entries) != 0 {
		t.Fatalf("partial record left: %#v", entries)
	}
	oversize := bytes.Repeat([]byte("a"), MaxHandoffBytes+1)
	if _, err := PutHandoff(vendors.AgentClaude, session, bytes.NewReader(oversize)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversize err = %v", err)
	}
	if entries, _ := os.ReadDir(handoffDir()); len(entries) != 0 {
		t.Fatalf("oversize left records: %#v", entries)
	}
}

func TestClaimRejectsExpiredAndBindingMismatch(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	session := "9c73be46-52af-4b1d-9ee7-123456789abc"
	id, err := PutHandoff(vendors.AgentClaude, session, strings.NewReader("brief"))
	if err != nil {
		t.Fatal(err)
	}
	path := handoffPath(id)
	record, err := readHandoffRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	record.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	payload, _ := json.Marshal(record)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimHandoff(vendors.AgentClaude, session, id); !errors.Is(err, ErrHandoffExpired) {
		t.Fatalf("expired err = %v", err)
	}

	id, err = PutHandoff(vendors.AgentClaude, session, strings.NewReader("brief"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimHandoff(vendors.AgentCodex, session, id); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("binding err = %v", err)
	}
}

func TestCleanupHandoffsSweepsExpiredBoundRecords(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	session := "9c73be46-52af-4b1d-9ee7-123456789abc"
	id, err := PutHandoff(vendors.AgentClaude, session, strings.NewReader("brief"))
	if err != nil {
		t.Fatal(err)
	}
	path := handoffPath(id)
	record, err := readHandoffRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	record.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	payload, _ := json.Marshal(record)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupHandoffs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired record remained")
	}
}

func TestSweepLeavesFreshUnreadableHandoff(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	if err := ensureHandoffDir(); err != nil {
		t.Fatal(err)
	}
	id := "h_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	path := handoffPath(id)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupHandoffs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("fresh unreadable record removed: %v", err)
	}
}

func TestWriteHandoffAtomicSurvivesConcurrentSweep(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	if err := ensureHandoffDir(); err != nil {
		t.Fatal(err)
	}
	session := "9c73be46-52af-4b1d-9ee7-123456789abc"
	id := "h_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	record := handoffRecord{
		Agent:     vendors.AgentClaude,
		SessionID: session,
		ExpiresAt: time.Now().Add(HandoffMaxAge).Unix(),
		Body:      handoffPreamble + "brief",
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = CleanupHandoffs()
		}
	}()
	if err := writeHandoffAtomic(handoffPath(id), payload); err != nil {
		t.Fatal(err)
	}
	<-done

	got, err := readHandoffRecord(handoffPath(id))
	if err != nil {
		t.Fatalf("published record missing after concurrent sweep: %v", err)
	}
	if got.Body != record.Body {
		t.Fatalf("body = %q", got.Body)
	}
}

func TestExecuteResumePassesPathsAsData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	cwd := filepath.Join(home, "proj; rm -rf /")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	session := "9c73be46-52af-4b1d-9ee7-123456789abc"

	originalLook, originalChdir, originalRun := lookPath, chdir, runAgent
	t.Cleanup(func() {
		lookPath, chdir, runAgent = originalLook, originalChdir, originalRun
	})
	lookPath = func(file string) (string, error) { return "/bin/" + file, nil }
	var sawChdir string
	chdir = func(dir string) error {
		sawChdir = dir
		return nil
	}
	var sawName string
	var sawArgs []string
	runAgent = func(name string, args []string) error {
		sawName = name
		sawArgs = append([]string{}, args...)
		return nil
	}

	if err := Execute(vendors.AgentClaude, cwd, session, ResumeSession, ""); err != nil {
		t.Fatal(err)
	}
	if sawChdir != cwd {
		t.Fatalf("chdir = %q", sawChdir)
	}
	if sawName != vendors.AgentClaude || len(sawArgs) != 2 || sawArgs[0] != "--resume" || sawArgs[1] != session {
		t.Fatalf("argv = %s %#v", sawName, sawArgs)
	}
}

func TestExecuteNewUsesHandoffWithoutShell(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	cwd := filepath.Join(home, "repo")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	session := "9c73be46-52af-4b1d-9ee7-123456789abc"
	hostile := "handoff $(reboot); rm -rf /"
	id, err := PutHandoff(vendors.AgentCodex, session, strings.NewReader(hostile))
	if err != nil {
		t.Fatal(err)
	}

	originalLook, originalChdir, originalRun := lookPath, chdir, runAgent
	t.Cleanup(func() {
		lookPath, chdir, runAgent = originalLook, originalChdir, originalRun
	})
	lookPath = func(file string) (string, error) { return "/bin/" + file, nil }
	chdir = func(string) error { return nil }
	var sawArgs []string
	runAgent = func(name string, args []string) error {
		sawArgs = append([]string{name}, args...)
		return nil
	}

	if err := Execute(vendors.AgentCodex, cwd, session, NewSession, id); err != nil {
		t.Fatal(err)
	}
	if len(sawArgs) != 3 || sawArgs[0] != vendors.AgentCodex || sawArgs[1] != "-c" {
		t.Fatalf("argv = %#v", sawArgs)
	}
	if !strings.HasPrefix(sawArgs[2], "developer_instructions=") {
		t.Fatalf("override = %q", sawArgs[2])
	}
	encoded := strings.TrimPrefix(sawArgs[2], "developer_instructions=")
	var decoded string
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decoded, hostile) {
		t.Fatalf("decoded handoff missing text: %q", decoded)
	}
	for _, arg := range sawArgs {
		if strings.Contains(arg, " ; ") || strings.Contains(arg, "&&") {
			t.Fatalf("shell syntax in argv: %#v", sawArgs)
		}
	}
}

func TestExecuteRejectsMissingSessionDirectory(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	originalLook := lookPath
	t.Cleanup(func() { lookPath = originalLook })
	lookPath = func(file string) (string, error) { return "/bin/" + file, nil }
	err := Execute(
		vendors.AgentClaude,
		filepath.Join(t.TempDir(), "missing"),
		"9c73be46-52af-4b1d-9ee7-123456789abc",
		ResumeSession,
		"",
	)
	if !errors.Is(err, ErrWorkingDirectory) {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalHandoffCommandStillBuilds(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	command, path, err := cliCommand(vendors.AgentClaude, "9c73be46-52af-4b1d-9ee7-123456789abc", ResumeSession, "")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("unexpected path %q", path)
	}
	if command != shellJoin("claude", "--resume", "9c73be46-52af-4b1d-9ee7-123456789abc") {
		t.Fatalf("command = %q", command)
	}
	command, path, err = cliCommand(vendors.AgentClaude, "", NewSession, "brief $(reboot)")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || !strings.Contains(command, "--append-system-prompt-file") {
		t.Fatalf("command=%q path=%q", command, path)
	}
	_ = os.Remove(path)
}
