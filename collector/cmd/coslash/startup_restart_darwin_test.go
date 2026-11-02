//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"golang.org/x/sys/unix"
)

// These tests run the real coslash executable against a private HOME and
// COSLASH_HOME. Hub, launchd, Launch Services and the keychain are never
// reached: no Hub is configured, a non-default COSLASH_HOME skips protocol
// registration, and every command found through PATH is a recording stub.
// launchctl is the only stub that can succeed, which simulates a loaded login
// agent; there is no real launchd supervision here.

const (
	fixtureAttemptID = "10000000-0000-4000-8000-000000000001"
	oldVersion       = "1.2.3"
	newVersion       = "1.2.4"
)

type startupFixture struct {
	t         *testing.T
	root      string
	home      string
	coslash   string
	target    string
	stubLog   string
	snapshot  string
	oldBinary string
	newBinary string
	childEnv  []string
}

var (
	collectorBuild    sync.Once
	collectorDir      string
	collectorBuildErr error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if collectorDir != "" {
		_ = os.RemoveAll(collectorDir)
	}
	os.Exit(code)
}

// collectorBinaries builds the real executable once per test run, with each
// version string linked in the way the release build does.
func collectorBinaries(t *testing.T) (oldPath, newPath string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the collector executable")
	}
	collectorBuild.Do(func() {
		collectorDir, collectorBuildErr = os.MkdirTemp("", "coslash-startup-")
		for _, version := range []string{oldVersion, newVersion} {
			if collectorBuildErr != nil {
				return
			}
			build := exec.Command("go", "build", "-o", filepath.Join(collectorDir, "coslash-"+version), "-ldflags", "-X main.version="+version, ".")
			if output, err := build.CombinedOutput(); err != nil {
				collectorBuildErr = fmt.Errorf("build collector %s: %w\n%s", version, err, output)
			}
		}
	})
	if collectorBuildErr != nil {
		t.Fatal(collectorBuildErr)
	}
	return filepath.Join(collectorDir, "coslash-"+oldVersion), filepath.Join(collectorDir, "coslash-"+newVersion)
}

func newStartupFixture(t *testing.T, launchctlStatus int) *startupFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldBinary, newBinary := collectorBinaries(t)
	f := &startupFixture{t: t, root: root, oldBinary: oldBinary, newBinary: newBinary}
	f.home = filepath.Join(root, "home")
	f.coslash = filepath.Join(root, "coslash-home")
	f.stubLog = filepath.Join(root, "stub.log")
	f.snapshot = filepath.Join(root, "queue-at-launchctl.json")
	stubs := filepath.Join(root, "stubs")
	for _, dir := range []string{f.home, f.coslash, stubs, filepath.Join(f.home, ".local", "bin")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"open", "security", "osascript", "osacompile", "lsregister", "scp", "sftp", "crontab", "systemctl", "ssh", "claude", "codex", "gemini", "opencode", "cursor-agent"} {
		f.writeStub(filepath.Join(stubs, name), "exit 97\n")
	}
	// launchctl records the call and snapshots the queue first, so the test can
	// see what had been persisted before the restart decision.
	f.writeStub(filepath.Join(stubs, "launchctl"), fmt.Sprintf(
		"[ -e %q ] || cp %q %q 2>/dev/null\nexit %d\n",
		f.snapshot, filepath.Join(f.coslash, "sync-v4", "queue.json"), f.snapshot, launchctlStatus))
	f.target = filepath.Join(f.home, ".local", "bin", "coslash")
	f.install(newBinary)
	f.childEnv = []string{
		"HOME=" + f.home, "USERPROFILE=" + f.home, "COSLASH_HOME=" + f.coslash,
		"TMPDIR=" + filepath.Join(root, "tmp"), "LANG=C",
		"XDG_CONFIG_HOME=" + filepath.Join(root, "xdg", "config"), "XDG_DATA_HOME=" + filepath.Join(root, "xdg", "data"),
		"PATH=" + stubs + ":/usr/bin:/bin",
	}
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.stopAll)
	return f
}

func (f *startupFixture) writeStub(path, body string) {
	script := fmt.Sprintf("#!/bin/sh\necho \"$(basename \"$0\") $*\" >> %q\n%s", f.stubLog, body)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		f.t.Fatal(err)
	}
}

func (f *startupFixture) install(binary string) {
	f.t.Helper()
	staged := f.target + ".staged"
	data, err := os.ReadFile(binary)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(staged, data, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(staged, f.target); err != nil {
		f.t.Fatal(err)
	}
}

// seedUpdate persists an update attempt exactly as a Hub command would.
func (f *startupFixture) seedUpdate(current string, legacy bool) {
	f.t.Helper()
	queue, err := syncv4.Open(filepath.Join(f.coslash, "sync-v4"))
	if err != nil {
		f.t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"attemptId": fixtureAttemptID, "targetVersion": newVersion,
		"expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "legacySettlement": legacy,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	command := hubclient.V4Command{ID: fixtureAttemptID, Type: "update", Payload: payload}
	if err := queue.AcceptUpdate(command, current, time.Now()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *startupFixture) update() *syncv4.UpdateAttempt {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.coslash, "sync-v4", "queue.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	return decodeUpdate(f.t, data)
}

func decodeUpdate(t *testing.T, data []byte) *syncv4.UpdateAttempt {
	t.Helper()
	var state struct {
		Update *syncv4.UpdateAttempt `json:"updateAttempt"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state.Update
}

func (f *startupFixture) start() *exec.Cmd {
	f.t.Helper()
	command := exec.Command(f.target, "--background", "--port", "0")
	command.Env = f.childEnv
	command.Dir = f.root
	if err := command.Start(); err != nil {
		f.t.Fatal(err)
	}
	return command
}

func waitFor(t *testing.T, what string, timeout time.Duration, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if condition() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func lockHeld(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		return err == unix.EWOULDBLOCK
	}
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	return false
}

func (f *startupFixture) ownerActive() bool {
	return lockHeld(filepath.Join(f.coslash, "runtime.lock")) && lockHeld(filepath.Join(f.coslash, "runtime.ready"))
}

// api waits for the runtime descriptor and returns the diagnostics version.
func (f *startupFixture) api(path string, authenticated bool) (int, []byte, error) {
	descriptor, err := os.ReadFile(filepath.Join(f.coslash, "runtime.json"))
	if err != nil {
		return 0, nil, err
	}
	var runtime struct{ BaseURL string }
	if err := json.Unmarshal(descriptor, &runtime); err != nil {
		return 0, nil, err
	}
	token, err := os.ReadFile(filepath.Join(f.coslash, "token"))
	if err != nil {
		return 0, nil, err
	}
	request, _ := http.NewRequest(http.MethodGet, runtime.BaseURL+path, nil)
	if authenticated {
		request.Header.Set("X-Coslash-Token", strings.TrimSpace(string(token)))
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response.StatusCode, body, err
}

func (f *startupFixture) serverVersion() string {
	status, body, err := f.api("/api/diagnostics", true)
	if err != nil || status != http.StatusOK {
		return ""
	}
	var diagnostics struct{ Version string }
	if json.Unmarshal(body, &diagnostics) != nil {
		return ""
	}
	return diagnostics.Version
}

func (f *startupFixture) waitReady(version string) {
	f.t.Helper()
	waitFor(f.t, "collector "+version+" readiness", 60*time.Second, func() bool {
		return f.ownerActive() && f.serverVersion() == version
	})
}

func (f *startupFixture) shutdown() {
	f.t.Helper()
	descriptor, err := os.ReadFile(filepath.Join(f.coslash, "runtime.json"))
	if err != nil {
		return
	}
	var runtime struct{ BaseURL string }
	token, tokenErr := os.ReadFile(filepath.Join(f.coslash, "token"))
	if json.Unmarshal(descriptor, &runtime) != nil || tokenErr != nil {
		return
	}
	request, _ := http.NewRequest(http.MethodPost, runtime.BaseURL+"/api/shutdown", nil)
	request.Header.Set("X-Coslash-Token", strings.TrimSpace(string(token)))
	if response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request); err == nil {
		response.Body.Close()
	}
}

// stopAll is the last resort: it terminates only processes running this
// fixture's own executable path.
func (f *startupFixture) stopAll() {
	f.shutdown()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && len(f.ownProcesses()) > 0 {
		time.Sleep(100 * time.Millisecond)
	}
	for _, pid := range f.ownProcesses() {
		_ = unix.Kill(pid, unix.SIGKILL)
	}
}

func (f *startupFixture) ownProcesses() []int {
	output, _ := exec.Command("/usr/bin/pgrep", "-f", "^"+regexp.QuoteMeta(f.target)+" ").Output()
	var pids []int
	for _, field := range strings.Fields(string(output)) {
		if pid, err := strconv.Atoi(field); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func (f *startupFixture) stubLines() []string {
	data, _ := os.ReadFile(f.stubLog)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// requireNoHostAccess fails if startup reached a command that could touch
// Launch Services, the keychain, login items or the network. Agent CLIs may only
// be probed for their version, and launchctl only queried.
func (f *startupFixture) requireNoHostAccess() {
	f.t.Helper()
	for _, line := range f.stubLines() {
		name, arguments, _ := strings.Cut(line, " ")
		switch name {
		case "":
		case "claude", "codex", "gemini", "opencode", "cursor-agent":
			if arguments != "--version" {
				f.t.Errorf("agent CLI was run beyond a version probe: %q", line)
			}
		case "launchctl":
			if !strings.HasPrefix(arguments, "print gui/") {
				f.t.Errorf("launchctl was used beyond a query: %q", line)
			}
		default:
			f.t.Errorf("startup reached %q", line)
		}
	}
}

func (f *startupFixture) installedVersion() string {
	output, err := exec.Command(f.target, "--version").Output()
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func (f *startupFixture) waitExit(command *exec.Cmd) {
	f.t.Helper()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			f.t.Fatalf("collector exited with %v", err)
		}
	case <-time.After(90 * time.Second):
		_ = command.Process.Kill()
		f.t.Fatal("collector did not exit")
	}
}

func TestNativeCollectorStartsAndStopsHermetically(t *testing.T) {
	f := newStartupFixture(t, 97)
	command := f.start()
	f.waitReady(newVersion)
	if status, _, err := f.api("/api/diagnostics", false); err != nil || status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status = %d, %v", status, err)
	}
	if status, body, err := f.api("/api/sync/status", true); err != nil || status != http.StatusOK || !strings.Contains(string(body), "not_connected") {
		t.Fatalf("sync status = %d %s %v", status, body, err)
	}
	f.shutdown()
	f.waitExit(command)
	if f.ownerActive() {
		t.Fatal("runtime locks are still held after shutdown")
	}
	if _, err := os.Stat(filepath.Join(f.coslash, "runtime.json")); !os.IsNotExist(err) {
		t.Fatalf("runtime descriptor was not removed: %v", err)
	}
	f.requireNoHostAccess()
}

func TestNativeCollectorSettlesLegacyUpdateAfterStartup(t *testing.T) {
	f := newStartupFixture(t, 97)
	f.seedUpdate(newVersion, true)
	backup := f.target + ".previous"
	if err := os.WriteFile(backup, []byte("not executed"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := f.start()
	f.waitReady(newVersion)
	waitFor(t, "legacy settlement", 30*time.Second, func() bool {
		update := f.update()
		return update != nil && update.HandoffReady
	})
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("settlement left the rollback backup: %v", err)
	}
	if update := f.update(); update.Stage != "finalizing" || update.Error != "" || !update.LegacySettlement {
		t.Fatalf("unexpected settled update %+v", update)
	}
	if f.installedVersion() != newVersion || !f.ownerActive() {
		t.Fatal("settlement must keep the running installed collector")
	}
	f.shutdown()
	f.waitExit(command)
	f.requireNoHostAccess()
}

func TestNativeCollectorConfirmsUpdateHandoffFromHelperReceipt(t *testing.T) {
	f := newStartupFixture(t, 97)
	f.seedUpdate(oldVersion, false)
	confirmation, err := os.OpenFile(filepath.Join(f.coslash, updateConfirmationLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer confirmation.Close()
	if err := unix.Flock(int(confirmation.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	command := f.start()
	f.waitReady(newVersion)
	if update := f.update(); update == nil || update.HandoffReady || update.Stage != "picked_up" {
		t.Fatalf("handoff must wait for the helper: %+v", update)
	}
	receipts := filepath.Join(f.coslash, "update-results")
	if err := os.MkdirAll(receipts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receipts, fixtureAttemptID), []byte("update_ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "update handoff confirmation", 30*time.Second, func() bool {
		update := f.update()
		return update != nil && update.HandoffReady
	})
	if _, err := os.Lstat(filepath.Join(receipts, fixtureAttemptID)); !os.IsNotExist(err) {
		t.Fatalf("receipt was not consumed: %v", err)
	}
	f.shutdown()
	f.waitExit(command)
	f.requireNoHostAccess()
}

func TestNativeLegacyRollbackRestartsInstalledCollector(t *testing.T) {
	t.Parallel()
	f := newStartupFixture(t, 97)
	f.runRollback()
	waitFor(t, "installed collector restart", 60*time.Second, func() bool {
		return f.ownerActive() && f.serverVersion() == oldVersion
	})
	if got := f.ownProcesses(); len(got) != 1 {
		t.Fatalf("expected one restarted collector from %s, found %v", f.target, got)
	}
	// The restored binary here is this source with an older version string, so it
	// understands the attempt. The real 0.4.2 predates updateAttempt and erases it.
	t.Logf("same-source stand-in's settlement pass left: %+v", f.update())
	f.shutdown()
	waitFor(t, "restarted collector shutdown", 30*time.Second, func() bool { return !f.ownerActive() })
	f.requireNoHostAccess()
}

func TestNativeLegacyRollbackDefersRestartToLoadedLogin(t *testing.T) {
	t.Parallel()
	f := newStartupFixture(t, 0)
	f.runRollback()
	time.Sleep(3 * time.Second)
	if f.ownerActive() || len(f.ownProcesses()) != 0 {
		t.Fatalf("collector restarted although the login agent is reported loaded: %v", f.ownProcesses())
	}
	f.requireNoHostAccess()
}

// runRollback starts the new collector while a simulated update helper holds
// the update lock, rolls the installed executable back the way the helper does,
// then lets the collector's settlement see the old executable. It returns once
// the first collector has exited and the pre-restart queue snapshot exists.
func (f *startupFixture) runRollback() {
	f.t.Helper()
	f.seedUpdate(newVersion, true)
	updateLock, err := os.OpenFile(filepath.Join(f.coslash, updateLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	defer updateLock.Close()
	if err := unix.Flock(int(updateLock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.t.Fatal(err)
	}
	command := f.start()
	f.waitReady(newVersion)
	backup := f.target + ".previous"
	if err := os.Link(f.oldBinary, backup); err != nil {
		f.t.Fatal(err)
	}
	if err := restoreUpdateTarget(f.target, backup); err != nil {
		f.t.Fatal(err)
	}
	if f.installedVersion() != oldVersion {
		f.t.Fatal("rollback did not install the old executable")
	}
	if update := f.update(); update == nil || update.Stage != "finalizing" || update.HandoffReady {
		f.t.Fatalf("settlement ran while the helper held the update lock: %+v", update)
	}
	if err := unix.Flock(int(updateLock.Fd()), unix.LOCK_UN); err != nil {
		f.t.Fatal(err)
	}
	f.waitExit(command)
	snapshot, err := os.ReadFile(f.snapshot)
	if err != nil {
		f.t.Fatalf("the restart decision did not query the login agent: %v", err)
	}
	if update := decodeUpdate(f.t, snapshot); update == nil || update.Stage != "failed" || update.Error != "update_failed" || update.HandoffReady || !update.LegacySettlement {
		f.t.Fatalf("rollback was not persisted before the restart decision: %+v", update)
	}
	if lines := f.stubLines(); len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "launchctl print gui/") {
		f.t.Fatalf("login agent was not queried: %v", lines)
	}
}
