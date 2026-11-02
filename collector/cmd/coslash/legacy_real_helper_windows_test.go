//go:build windows && realhelper

package main

// Native Windows proof against the REAL published v0.4.2 update helper.
//
// This file is compiled only with `-tags realhelper` and is driven by
// collector/scripts/windows-real-helper.ps1, which downloads the immutable
// v0.4.2 release asset, verifies it against testdata/legacy-helper-0.4.2-
// windows-amd64.sha256 and builds the new target. Nothing here downloads
// anything. Every process, including every `--version` check, runs with owned
// HOME, USERPROFILE, COSLASH_HOME, LOCALAPPDATA, APPDATA, TEMP and TMP, a
// minimal system-only environment, no credentials and no Hub, so no network,
// startup registration or user data is involved.
//
// Protocol, verified against 8d68f64 collector/cmd/coslash/auto_update.go
// runUpdateApply: the helper is `coslash.exe update-apply <target> <staged>
// <ready>` with EXACTLY three arguments (any other count returns at once and
// does nothing; one scenario pins that). All three must be absolute and in the
// same directory, staged must be named `.coslash-update-*`, and ready must be
// staged + ".ready". The old helper:
//   - takes update.lock (exclusive LockFileEx) and only then writes "ready";
//   - waits up to a minute while runtimeOwnerActive (runtime.lock AND
//     runtime.ready both exclusively held), then copies target to
//     target+".previous", moves staged over target (MoveFileEx) and closes
//     update.lock;
//   - starts `target --background` and, for 30 s polling every 200 ms, retires
//     the backup (os.Remove) and returns if a runtime owner is active; else it
//     restores backup over target (MoveFileEx) and starts the target again;
//   - always removes the staged file, schedules a hidden powershell.exe to
//     delete its own copy, and exits 0 (every path returns normally).
// It writes NO update-results receipts (that is the 0.4.3 helper) and logs only
// failures, to stderr: "stage ...", "release ... update handoff", "restore
// previous coSlash Local after launch failure" and "... after readiness
// timeout". A restore that succeeds is silent, so the observable evidence of a
// rollback is the target's bytes, the backup being consumed and the new
// collector's own background log, not a helper log line.
//
// The new collector's settlement code (settleLegacyTarget and
// retireLegacyBackup) runs in this test process, not inside the target image;
// see the handoff for what that does and does not cover.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	realHelperLegacyVersion = "0.4.2"
	realHelperNewVersion    = "0.4.3"
	// The 0.4.2 helper polls this long after starting the new collector before
	// it rolls back. It is used only to position the collision scenarios'
	// attempts; no assertion depends on it being exact.
	realHelperRestoreDelay = 30 * time.Second
	realHelperRefusedText  = "being used by another process"
)

var realHelperSentinels = map[string]string{
	"sentinel-identity.json":    `{"deviceId":"realhelper-identity"}` + "\n",
	"sentinel-queue.json":       `{"pending":["realhelper-queue-entry"]}` + "\n",
	"sentinel-preferences.json": `{"autoUpdate":false}` + "\n",
	"sentinel-credentials":      "realhelper-credential-placeholder\n",
}

type realHelperBinaries struct {
	legacy, next       string
	legacySHA, nextSHA string
}

func realHelperFileSHA(t *testing.T, name string) string {
	t.Helper()
	file, err := os.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		t.Fatalf("hash %s: %v", name, err)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// realHelperLayout is one owned fixture tree. Nothing outside it is ever
// written, and every child process sees only it.
type realHelperLayout struct {
	root, home, profile, appData, localAppData, install, tmp, logs string
}

func newRealHelperLayout(root string) (realHelperLayout, error) {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	l := realHelperLayout{root: root}
	l.home = filepath.Join(root, "home")
	l.profile = filepath.Join(root, "profile")
	l.appData = filepath.Join(root, "AppData")
	l.localAppData = filepath.Join(root, "LocalAppData")
	// The target lives where the real Windows installer puts it, so the real
	// install-channel detector sees `windows-script`.
	l.install = filepath.Join(l.localAppData, "coSlash", "bin")
	l.tmp = filepath.Join(root, "tmp")
	l.logs = filepath.Join(root, "logs")
	for _, dir := range []string{l.home, l.profile, l.appData, l.install, l.tmp, l.logs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return l, err
		}
	}
	return l, nil
}

// env is the complete environment of every child, the `--version` checks
// included: nothing ambient except non-secret system variables Windows needs to
// start processes and the helper's own hidden powershell.exe cleanup. PATH is
// rebuilt from the system directories only. COSLASH_SYNC_PAUSED is belt and
// braces: no Hub URL, identity or credential exists, so the sync loop has
// nothing to contact anyway.
func (l realHelperLayout) env() []string {
	system := os.Getenv("SystemRoot")
	env := []string{
		"COSLASH_HOME=" + l.home,
		"COSLASH_SYNC_PAUSED=1",
		"HOME=" + l.profile,
		"USERPROFILE=" + l.profile,
		"APPDATA=" + l.appData,
		"LOCALAPPDATA=" + l.localAppData,
		"TEMP=" + l.tmp,
		"TMP=" + l.tmp,
		"PATH=" + filepath.Join(system, "System32") + ";" + system + ";" + filepath.Join(system, "System32", "WindowsPowerShell", "v1.0"),
	}
	for _, name := range []string{"SystemRoot", "windir", "SystemDrive", "ComSpec", "PATHEXT", "ProgramData", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "ALLUSERSPROFILE", "PUBLIC", "COMPUTERNAME", "OS", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS", "USERNAME", "USERDOMAIN"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// realHelperVersion runs `<executable> --version` with the owned environment
// and working directory. The product readExecutableVersion inherits the
// ambient environment, so the proof does not use it for any child.
func realHelperVersion(l realHelperLayout, executable string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "--version")
	command.Env, command.Dir = l.env(), l.tmp
	output, err := command.Output()
	if err != nil || len(output) > 256 {
		return "", fmt.Errorf("cannot verify executable version: %v", err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(output)), "v"), nil
}

// requireRealHelperBinaries fails (it never skips) unless the legacy helper is
// byte-identical to the pinned official release asset and both binaries report
// the expected versions, checked in an isolated environment.
func requireRealHelperBinaries(t *testing.T) realHelperBinaries {
	t.Helper()
	if os.Getenv("SystemRoot") == "" {
		t.Fatal("SystemRoot is not set; cannot build the isolated child environment")
	}
	pinned, err := os.ReadFile(filepath.Join("testdata", "legacy-helper-0.4.2-windows-amd64.sha256"))
	if err != nil {
		t.Fatalf("read pinned checksum: %v", err)
	}
	fields := strings.Fields(string(pinned))
	if len(fields) != 2 || len(fields[0]) != 64 || fields[1] != "coslash-windows-amd64.exe" {
		t.Fatalf("pinned checksum file is malformed: %q", pinned)
	}
	b := realHelperBinaries{legacy: os.Getenv("COSLASH_REAL_HELPER_V042"), next: os.Getenv("COSLASH_REAL_HELPER_NEW")}
	for _, name := range []string{b.legacy, b.next} {
		info, err := os.Stat(name)
		if name == "" || !filepath.IsAbs(name) || err != nil || !info.Mode().IsRegular() {
			t.Fatalf("COSLASH_REAL_HELPER_V042 and COSLASH_REAL_HELPER_NEW must name absolute regular files (got %q)", name)
		}
	}
	b.legacySHA, b.nextSHA = realHelperFileSHA(t, b.legacy), realHelperFileSHA(t, b.next)
	if b.legacySHA != fields[0] {
		t.Fatalf("legacy helper sha256 %s does not match the pinned official v0.4.2 asset %s", b.legacySHA, fields[0])
	}
	if b.nextSHA == b.legacySHA {
		t.Fatal("new target is identical to the legacy helper")
	}
	scratch, err := os.MkdirTemp("", "coslash-real-helper-version-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	layout, err := newRealHelperLayout(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := realHelperVersion(layout, b.legacy); err != nil || got != realHelperLegacyVersion {
		t.Fatalf("legacy helper reports version %q (%v), want %s", got, err, realHelperLegacyVersion)
	}
	if got, err := realHelperVersion(layout, b.next); err != nil || got != realHelperNewVersion {
		t.Fatalf("new target reports version %q (%v), want %s", got, err, realHelperNewVersion)
	}
	return b
}

type realHelperFixture struct {
	realHelperLayout
	t                             *testing.T
	bins                          realHelperBinaries
	target, backup, staged, ready string
	helperPath                    string
	runtimeLock                   *os.File
}

func newRealHelperFixture(t *testing.T, bins realHelperBinaries) *realHelperFixture {
	t.Helper()
	root, err := os.MkdirTemp("", "coslash-real-helper-")
	if err != nil {
		t.Fatal(err)
	}
	f := &realHelperFixture{t: t, bins: bins}
	t.Cleanup(f.cleanup)
	if f.realHelperLayout, err = newRealHelperLayout(root); err != nil {
		t.Fatal(err)
	}
	f.target = filepath.Join(f.install, "coslash.exe")
	f.backup = f.target + ".previous"
	f.staged = filepath.Join(f.install, ".coslash-update-realhelper.exe")
	f.ready = f.staged + ".ready"
	f.helperPath = filepath.Join(f.tmp, "coslash-update-helper-realhelper.exe")
	f.copyFile(bins.legacy, f.target)
	f.copyFile(bins.next, f.staged)
	for name, body := range realHelperSentinels {
		if err := os.WriteFile(filepath.Join(f.home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *realHelperFixture) copyFile(src, dst string) {
	f.t.Helper()
	in, err := os.Open(src)
	if err != nil {
		f.t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		f.t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	if err := errors.Join(copyErr, out.Close()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *realHelperFixture) cleanup() {
	if f.runtimeLock != nil {
		_ = f.runtimeLock.Close()
	}
	if f.root == "" {
		return
	}
	var err error
	for attempt := 0; attempt < 40; attempt++ {
		realHelperProcessesUnder([]string{f.install, f.tmp}, true)
		if err = os.RemoveAll(f.root); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	f.t.Logf("could not remove owned fixture %s: %v", f.root, err)
}

func (f *realHelperFixture) version(name string) (string, error) {
	return realHelperVersion(f.realHelperLayout, name)
}

// lockHeld mirrors the product's own probe (exclusiveRuntimeLockHeld): a
// non-blocking lock attempt through a fresh handle that fails only if someone
// else holds the byte-range lock.
func (f *realHelperFixture) lockHeld(name string) bool {
	file, err := os.OpenFile(filepath.Join(f.home, name), os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	overlapped := &windows.Overlapped{}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped); err != nil {
		return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
	}
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
	return false
}

// ownerActive is runtimeOwnerActive for the fixture's home: the same condition
// the 0.4.2 helper polls (runtime.lock and runtime.ready both held).
func (f *realHelperFixture) ownerActive() bool {
	return f.lockHeld("runtime.lock") && f.lockHeld(readinessFilename)
}

// holdRuntimeLock keeps runtime.lock exclusively locked for the whole test, so
// a collector started by the helper exits at once (acquireRuntimeLock fails)
// and the helper never sees a ready runtime owner (it needs runtime.ready too).
// That is the supported way to make the helper reach its readiness timeout and
// roll back, with no helper modification.
func (f *realHelperFixture) holdRuntimeLock() {
	f.t.Helper()
	file, err := os.OpenFile(filepath.Join(f.home, "runtime.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		file.Close()
		f.t.Fatalf("lock runtime.lock: %v", err)
	}
	f.runtimeLock = file
}

type realHelperRun struct {
	cmd           *exec.Cmd
	logPath       string
	exited        chan struct{}
	exitedAt      time.Time
	err           error
	expectCleanup bool
}

// log is the helper's stderr/stdout. The 0.4.2 helper logs failures only; a
// successful restore is silent.
func (r *realHelperRun) log() string {
	body, _ := os.ReadFile(r.logPath)
	return string(body)
}

// launchHelper starts a copy of the installed 0.4.2 executable from the owned
// TEMP, exactly as launchUpdateHelper does, with the given arguments after
// `update-apply`. Output goes to a file, not a pipe: the helper's hidden
// powershell.exe cleanup inherits stdout and would otherwise keep a pipe open
// (and Wait blocked) after the helper itself has exited.
func (f *realHelperFixture) launchHelper(args ...string) *realHelperRun {
	f.t.Helper()
	f.copyFile(f.bins.legacy, f.helperPath)
	run := &realHelperRun{logPath: filepath.Join(f.logs, "helper.log"), exited: make(chan struct{}), expectCleanup: len(args) == 3}
	logFile, err := os.OpenFile(run.logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	run.cmd = exec.Command(f.helperPath, append([]string{"update-apply"}, args...)...)
	run.cmd.Env, run.cmd.Dir = f.env(), f.tmp
	run.cmd.Stdout, run.cmd.Stderr = logFile, logFile
	err = run.cmd.Start()
	_ = logFile.Close()
	if err != nil {
		f.t.Fatalf("start real v0.4.2 helper: %v", err)
	}
	go func() {
		run.err = run.cmd.Wait()
		run.exitedAt = time.Now()
		close(run.exited)
	}()
	f.t.Cleanup(func() {
		select {
		case <-run.exited:
		default:
			_ = run.cmd.Process.Kill()
			<-run.exited
		}
	})
	return run
}

// startHelper launches the helper with the three arguments the 0.4.2
// collector passes and performs the same ready-file handshake. It also checks
// what the old helper promises before it writes "ready": update.lock is held.
func (f *realHelperFixture) startHelper() *realHelperRun {
	f.t.Helper()
	run := f.launchHelper(f.target, f.staged, f.ready)
	deadline := time.Now().Add(20 * time.Second)
	for {
		state, err := os.ReadFile(f.ready)
		if err == nil && strings.TrimSpace(string(state)) != "" {
			if strings.TrimSpace(string(state)) != "ready" {
				f.t.Fatalf("real helper could not claim the handoff (%q): %s", state, run.log())
			}
			if !f.lockHeld("update.lock") {
				f.t.Fatalf("real helper reported ready without holding update.lock: %s", run.log())
			}
			_ = os.Remove(f.ready)
			return run
		}
		select {
		case <-run.exited:
			f.t.Fatalf("real helper exited before readiness (%v): %s", run.err, run.log())
		default:
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("real helper never wrote readiness: %s", run.log())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitSwap returns once the helper's MoveFileEx(staged -> target) has
// completed: the staged file is gone and the helper's backup exists. The
// helper creates the backup before the move and the move consumes the staged
// file, so this is a state barrier, not a sleep. Nothing here opens the target
// (a running target would block the helper's replace). It also requires the
// helper to release update.lock right after, which the old helper does before
// it starts polling for readiness.
func (f *realHelperFixture) waitSwap(run *realHelperRun) time.Time {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, stagedErr := os.Lstat(f.staged)
		_, backupErr := os.Lstat(f.backup)
		if errors.Is(stagedErr, os.ErrNotExist) && backupErr == nil {
			break
		}
		select {
		case <-run.exited:
			f.t.Fatalf("real helper finished before the swap was observed (%v): %s", run.err, run.log())
		default:
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("swap was not observed: %s", run.log())
		}
		time.Sleep(5 * time.Millisecond)
	}
	swapped := time.Now()
	for f.lockHeld("update.lock") {
		select {
		case <-run.exited:
			f.t.Fatalf("real helper exited while update.lock still looked held: %s", run.log())
		default:
		}
		if time.Since(swapped) > 10*time.Second {
			f.t.Fatalf("real helper did not release update.lock after the swap: %s", run.log())
		}
		time.Sleep(5 * time.Millisecond)
	}
	return swapped
}

// waitExit waits for the helper process, then for the powershell.exe it
// schedules to delete the helper's own copy (observable, and required: a copy
// left behind would be a leak in the user's TEMP).
func (f *realHelperFixture) waitExit(run *realHelperRun) {
	f.t.Helper()
	select {
	case <-run.exited:
	case <-time.After(2 * time.Minute):
		f.t.Fatalf("real helper did not exit: %s", run.log())
	}
	if run.err != nil {
		f.t.Fatalf("real helper exited abnormally: %v: %s", run.err, run.log())
	}
	if !run.expectCleanup {
		return
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err := os.Lstat(f.helperPath); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("real helper's scheduled cleanup never deleted its copy: %s", run.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// requireHelperSilent: the 0.4.2 helper writes nothing on its success and
// rollback-success paths, so any "coSlash Local" failure line is a defect.
func (f *realHelperFixture) requireHelperSilent(run *realHelperRun) {
	f.t.Helper()
	if log := run.log(); strings.Contains(log, "coSlash Local") {
		f.t.Fatalf("real helper logged a failure: %s", log)
	}
}

func (f *realHelperFixture) settle(readVersion func(string) (string, error)) error {
	return settleLegacyTarget(f.target, realHelperNewVersion, readVersion)
}

// requireInstalled asserts the installed executable is a regular file whose
// bytes and reported version are exactly one of the two known binaries.
func (f *realHelperFixture) requireInstalled(wantNew bool) {
	f.t.Helper()
	info, err := os.Lstat(f.target)
	if err != nil || !info.Mode().IsRegular() {
		f.t.Fatalf("installed executable is missing or not regular: %v", err)
	}
	wantSHA, wantVersion := f.bins.legacySHA, realHelperLegacyVersion
	if wantNew {
		wantSHA, wantVersion = f.bins.nextSHA, realHelperNewVersion
	}
	if got := realHelperFileSHA(f.t, f.target); got != wantSHA {
		f.t.Fatalf("installed executable sha256 %s, want %s (new=%v)", got, wantSHA, wantNew)
	}
	if got, err := f.version(f.target); err != nil || got != wantVersion {
		f.t.Fatalf("installed executable reports %q (%v), want %s", got, err, wantVersion)
	}
}

func (f *realHelperFixture) requireClean() {
	f.t.Helper()
	if _, err := os.Lstat(f.backup); !errors.Is(err, os.ErrNotExist) {
		f.t.Fatalf("backup still exists or is unreadable: %v", err)
	}
	for _, pattern := range []string{".coslash-restore-*", ".coslash-update-*"} {
		if matches, _ := filepath.Glob(filepath.Join(f.install, pattern)); len(matches) != 0 {
			f.t.Fatalf("stray update files left behind: %v", matches)
		}
	}
	for name, want := range realHelperSentinels {
		got, err := os.ReadFile(filepath.Join(f.home, name))
		if err != nil || string(got) != want {
			f.t.Fatalf("identity/queue/preference sentinel %s changed: %q (%v)", name, got, err)
		}
	}
}

// waitNoProcesses waits until no process runs from the owned install or TEMP
// directories (the collectors the helper launches exit at once when
// runtime.lock is held).
func (f *realHelperFixture) waitNoProcesses() {
	f.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for realHelperProcessesUnder([]string{f.install, f.tmp}, false) != 0 {
		if time.Now().After(deadline) {
			f.t.Fatal("a process launched by the helper is still running")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (f *realHelperFixture) backgroundLogCount(text string) int {
	body, _ := os.ReadFile(filepath.Join(f.home, "logs", backgroundLogName))
	return strings.Count(string(body), text)
}

func TestRealLegacyHelper(t *testing.T) {
	bins := requireRealHelperBinaries(t)
	t.Logf("legacy helper sha256 %s (pinned official v0.4.2); new target sha256 %s", bins.legacySHA, bins.nextSHA)
	scenarios := []struct {
		name string
		run  func(*testing.T, realHelperBinaries)
	}{
		{"FourArgumentFormIsIgnoredByRealHelper", testRealHelperFourArguments},
		{"ReadyCollectorHelperRetiresItsOwnBackup", testRealHelperReady},
		{"RollbackWins", testRealHelperRollbackWins},
		{"SettlementWinsBeforeRollback", testRealHelperSettlementWins},
		{"SettlementHoldsBackupAcrossRealRollbackAttempt", testRealHelperHoldAcrossRestore},
	}
	// Collision stress: one settlement attempt per scenario, started at a
	// different offset around the helper's expected rollback instant.
	for _, offset := range []time.Duration{-1000 * time.Millisecond, -400 * time.Millisecond, -150 * time.Millisecond, 0, 150 * time.Millisecond, 400 * time.Millisecond} {
		offset := offset
		scenarios = append(scenarios, struct {
			name string
			run  func(*testing.T, realHelperBinaries)
		}{fmt.Sprintf("CollisionWindow/%+dms", offset.Milliseconds()), func(t *testing.T, b realHelperBinaries) { testRealHelperCollision(t, b, offset) }})
	}
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			scenario.run(t, bins)
		})
	}
}

// The published 0.4.2 helper accepts exactly three arguments. The 0.4.3
// four-argument form (with an attempt ID) is silently ignored: no lock, no
// ready file, no swap. This pins the protocol the rest of the harness drives.
func testRealHelperFourArguments(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	run := f.launchHelper(f.target, f.staged, f.ready, "0b7f6b3e-5a53-4a52-9d52-6a3b0a8c1f10")
	f.waitExit(run)
	if _, err := os.Lstat(f.ready); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ignored helper wrote a ready file: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.home, "update.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ignored helper took update.lock: %v", err)
	}
	if _, err := os.Lstat(f.backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ignored helper created a backup: %v", err)
	}
	if got := realHelperFileSHA(t, f.staged); got != bins.nextSHA {
		t.Fatalf("ignored helper changed the staged file: %s", got)
	}
	f.requireInstalled(false)
	f.requireHelperSilent(run)
}

// The new collector becomes the runtime owner, so the real helper itself
// retires its backup. Settlement afterwards must be a clean no-op.
func testRealHelperReady(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	run := f.startHelper()
	f.waitExit(run)
	f.requireHelperSilent(run)
	if !f.ownerActive() {
		t.Fatalf("helper exited without a running runtime owner (the new collector): %s", run.log())
	}
	descriptor, err := os.ReadFile(filepath.Join(f.home, runtimeFilename))
	if err != nil {
		t.Fatalf("new collector never published its runtime descriptor: %v", err)
	}
	var parsed runtimeDescriptor
	if err := json.Unmarshal(descriptor, &parsed); err != nil {
		t.Fatalf("runtime descriptor is not JSON: %s", descriptor)
	}
	if base, err := url.Parse(parsed.BaseURL); err != nil || base.Scheme != "http" || !loopbackHost(base.Hostname()) {
		t.Fatalf("runtime descriptor is not a loopback http URL: %s", descriptor)
	}
	f.requireInstalled(true)
	if err := f.settle(f.version); err != nil {
		t.Fatalf("settlement after the helper kept the update: %v", err)
	}
	realHelperProcessesUnder([]string{f.install, f.tmp}, true)
	f.waitNoProcesses()
	f.requireClean()
}

// The helper reaches its readiness timeout and rolls back. The old helper
// leaves no receipt; the evidence is the restored 0.4.2 bytes, the consumed
// backup and two collector launches (the new one and the restored old one) that
// both stopped at the held runtime.lock. Settlement must then report the
// changed installation and leave the restored 0.4.2 intact.
func testRealHelperRollbackWins(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	f.holdRuntimeLock()
	run := f.startHelper()
	f.waitSwap(run)
	f.waitExit(run)
	f.waitNoProcesses()
	f.requireHelperSilent(run)
	f.requireInstalled(false)
	if got := f.backgroundLogCount(errRuntimeAlreadyRunning.Error()); got < 2 {
		t.Fatalf("expected the new collector and the restored 0.4.2 collector to each stop at the held runtime lock, saw %d background log entries", got)
	}
	if err := f.settle(f.version); !errors.Is(err, errLegacyInstalledChanged) {
		t.Fatalf("settlement after real rollback returned %v, want errLegacyInstalledChanged", err)
	}
	f.requireInstalled(false)
	f.requireClean()
}

// Settlement retires the backup right after the real swap, long before the
// helper's rollback attempt. The helper's later restore must fail on a missing
// backup (it logs that), not on a sharing violation, and leave the new target.
func testRealHelperSettlementWins(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	f.holdRuntimeLock()
	run := f.startHelper()
	f.waitSwap(run)
	if err := f.settle(f.version); err != nil {
		t.Fatalf("settlement after swap: %v", err)
	}
	f.waitExit(run)
	f.waitNoProcesses()
	log := run.log()
	if !strings.Contains(log, "restore previous coSlash Local after readiness timeout") {
		t.Fatalf("real helper did not attempt its rollback after settlement: %s", log)
	}
	if strings.Contains(log, realHelperRefusedText) {
		t.Fatalf("rollback after settlement failed by sharing violation, not a missing backup: %s", log)
	}
	t.Logf("helper rollback attempt after settlement won: %s", strings.TrimSpace(log))
	f.requireInstalled(true)
	f.requireClean()
}

// Deterministic barrier: settlement's version check under the backup hold does
// not return until the real helper process has exited, so the helper's one
// rollback attempt is guaranteed to happen while the hold is active. The hold
// must make it fail with a sharing violation, and the installation must stay
// at the new version.
func testRealHelperHoldAcrossRestore(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	f.holdRuntimeLock()
	run := f.startHelper()
	f.waitSwap(run)
	calls := 0
	read := func(name string) (string, error) {
		calls++
		// Windows settlement checks the version before the hold, under the
		// hold, and after retirement. The second call is under the hold.
		if calls == 2 {
			select {
			case <-run.exited:
			case <-time.After(2 * time.Minute):
				return "", errors.New("legacy helper never finished its rollback attempt")
			}
		}
		return f.version(name)
	}
	if err := f.settle(read); err != nil {
		t.Fatalf("settlement holding the backup across the helper's rollback: %v; helper output: %s", err, run.log())
	}
	if calls != 3 {
		t.Fatalf("settlement read the version %d times, want 3 (before, under hold, after)", calls)
	}
	f.waitExit(run)
	f.waitNoProcesses()
	log := run.log()
	if !strings.Contains(log, "restore previous coSlash Local after readiness timeout") || !strings.Contains(strings.ToLower(log), realHelperRefusedText) {
		t.Fatalf("real helper's rollback attempt was not refused by the backup hold (sharing violation expected): %s", log)
	}
	t.Logf("helper rollback attempt under the hold: %s", strings.TrimSpace(log))
	f.requireInstalled(true)
	f.requireClean()
}

// Probabilistic collision stress. The helper restores about 30 s after it
// started the new collector (polling every 200 ms), so a single settlement
// attempt started at swap + 30 s + offset lands before, inside or after the
// helper's restore depending on offset and machine speed; the offsets are
// spread to straddle it. Whatever the interleaving (settled first, rollback
// refused by the hold, or rollback first), the installation must exist, match
// one known binary byte for byte, agree with the settlement result, and the
// helper's own log must agree with the outcome. It does not pin an
// interleaving, and the log lines record which one actually happened.
func testRealHelperCollision(t *testing.T, bins realHelperBinaries, offset time.Duration) {
	f := newRealHelperFixture(t, bins)
	f.holdRuntimeLock()
	run := f.startHelper()
	swapped := f.waitSwap(run)
	select {
	case <-time.After(time.Until(swapped.Add(realHelperRestoreDelay + offset))):
	case <-run.exited:
	}
	var result error
	attempts, afterExit := 0, 0
	first := time.Since(swapped)
	for {
		attempts++
		result = f.settle(f.version)
		if result == nil || errors.Is(result, errLegacyInstalledChanged) {
			break
		}
		select {
		case <-run.exited:
			if afterExit++; afterExit > 3 {
				t.Fatalf("settlement never became terminal after the helper exited: %v; helper output: %s", result, run.log())
			}
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	last := time.Since(swapped)
	f.waitExit(run)
	f.waitNoProcesses()
	log := run.log()
	restoreFailed := strings.Contains(log, "restore previous coSlash Local after readiness timeout")
	label := "settlement-wins (backup retired before the helper's restore)"
	switch {
	case result == nil && !restoreFailed:
		t.Fatalf("settlement succeeded but the helper logged no failed restore: %s", log)
	case result == nil && strings.Contains(strings.ToLower(log), realHelperRefusedText):
		label = "hold-refused-rollback (helper's restore hit the share-none hold)"
	case result != nil && restoreFailed:
		t.Fatalf("settlement reported a rollback but the helper logged a failed restore: %s", log)
	case result != nil:
		label = "rollback-wins (helper restored before settlement could retire the backup)"
	}
	t.Logf("collision offset %+dms: %s; %d attempt(s), first at swap%+dms, last ended swap%+dms, helper exited swap%+dms; helper log: %q",
		offset.Milliseconds(), label, attempts, first.Milliseconds(), last.Milliseconds(), run.exitedAt.Sub(swapped).Milliseconds(), strings.TrimSpace(log))
	f.requireInstalled(result == nil)
	f.requireClean()
}

// realHelperProcessesUnder counts the live processes whose image lives inside
// one of the owned fixture directories and, if asked, terminates them. Only
// such processes are ever touched.
func realHelperProcessesUnder(dirs []string, terminate bool) int {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if windows.Process32First(snapshot, &entry) != nil {
		return 0
	}
	count := 0
	for {
		if entry.ProcessID != 0 && realHelperProcessUnder(entry.ProcessID, dirs, terminate) {
			count++
		}
		if windows.Process32Next(snapshot, &entry) != nil {
			return count
		}
	}
}

func realHelperProcessUnder(pid uint32, dirs []string, terminate bool) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	if event, _ := windows.WaitForSingleObject(handle, 0); event == windows.WAIT_OBJECT_0 {
		return false // already exited
	}
	buffer := make([]uint16, 4096)
	size := uint32(len(buffer))
	if windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size) != nil {
		return false
	}
	image := strings.ToLower(windows.UTF16ToString(buffer[:size]))
	for _, dir := range dirs {
		if dir != "" && strings.HasPrefix(image, strings.ToLower(dir)+`\`) {
			if terminate {
				_ = windows.TerminateProcess(handle, 1)
				_, _ = windows.WaitForSingleObject(handle, 5000)
			}
			return true
		}
	}
	return false
}
