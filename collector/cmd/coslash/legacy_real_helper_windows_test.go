//go:build windows && realhelper

package main

// Native Windows proof against the REAL published v0.4.2 update helper.
//
// This file is compiled only with `-tags realhelper` and is driven by
// collector/scripts/windows-real-helper.ps1, which downloads the immutable
// v0.4.2 release asset, verifies it against testdata/legacy-helper-0.4.2-
// windows-amd64.sha256 and builds the new target. Nothing here downloads
// anything. Every process runs with owned HOME, USERPROFILE, COSLASH_HOME,
// LOCALAPPDATA, APPDATA, TEMP and TMP, an otherwise empty environment, no
// credentials and no Hub, so no network, startup registration or user data is
// involved.
//
// The helper under test is `coslash.exe update-apply <target> <staged> <ready>
// <attempt>`, the exact command the 0.4.2 collector launches. The new
// collector's settlement code (settleLegacyTarget and retireLegacyBackup) runs
// in this test process, not inside the target image; see the handoff for what
// that does and does not cover.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	realHelperLegacyVersion = "0.4.2"
	realHelperNewVersion    = "0.4.3"
	realHelperAttemptID     = "0b7f6b3e-5a53-4a52-9d52-6a3b0a8c1f10"
	// The 0.4.2 helper waits this long (8d68f64, auto_update.go, the readiness
	// loop after starting the new collector) before it rolls back. It is used
	// only to position the collision test's attempts; no assertion depends on
	// it being exact.
	realHelperRestoreDelay = 30 * time.Second
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

func realHelperReadVersion(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return readExecutableVersion(ctx, name)
}

// requireRealHelperBinaries fails (it never skips) unless the legacy helper is
// byte-identical to the pinned official release asset and both binaries report
// the expected versions.
func requireRealHelperBinaries(t *testing.T) realHelperBinaries {
	t.Helper()
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
	if got, err := realHelperReadVersion(b.legacy); err != nil || got != realHelperLegacyVersion {
		t.Fatalf("legacy helper reports version %q (%v), want %s", got, err, realHelperLegacyVersion)
	}
	if got, err := realHelperReadVersion(b.next); err != nil || got != realHelperNewVersion {
		t.Fatalf("new target reports version %q (%v), want %s", got, err, realHelperNewVersion)
	}
	return b
}

type realHelperLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *realHelperLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *realHelperLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type realHelperFixture struct {
	t                                 *testing.T
	bins                              realHelperBinaries
	root, home, profile, localAppData string
	install, tmp                      string
	target, backup, staged, ready     string
	runtimeLock                       *os.File
}

func newRealHelperFixture(t *testing.T, bins realHelperBinaries) *realHelperFixture {
	t.Helper()
	root, err := os.MkdirTemp("", "coslash-real-helper-")
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	f := &realHelperFixture{t: t, bins: bins, root: root}
	f.home = filepath.Join(root, "home")
	f.profile = filepath.Join(root, "profile")
	f.localAppData = filepath.Join(root, "LocalAppData")
	f.install = filepath.Join(f.localAppData, "coSlash", "bin")
	f.tmp = filepath.Join(root, "tmp")
	for _, dir := range []string{f.home, f.profile, f.install, f.tmp, filepath.Join(root, "AppData")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.target = filepath.Join(f.install, "coslash.exe")
	f.backup = f.target + ".previous"
	f.staged = filepath.Join(f.install, ".coslash-update-realhelper.exe")
	f.ready = f.staged + ".ready"
	t.Cleanup(f.cleanup)
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
	realHelperKillProcessesUnder(f.install, f.tmp)
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = os.RemoveAll(f.root); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	f.t.Logf("could not remove owned fixture %s: %v", f.root, err)
}

// env is the complete environment of every child: nothing ambient except what
// Windows needs to start processes and powershell.exe (used by the 0.4.2
// helper's own cleanup).
func (f *realHelperFixture) env() []string {
	env := []string{
		"COSLASH_HOME=" + f.home,
		"HOME=" + f.profile,
		"USERPROFILE=" + f.profile,
		"APPDATA=" + filepath.Join(f.root, "AppData"),
		"LOCALAPPDATA=" + f.localAppData,
		"TEMP=" + f.tmp,
		"TMP=" + f.tmp,
	}
	for _, name := range []string{"SystemRoot", "windir", "SystemDrive", "ComSpec", "PATHEXT", "PATH"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
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
	cmd    *exec.Cmd
	log    *realHelperLog
	exited chan struct{}
	err    error
}

// startHelper launches the real 0.4.2 helper exactly as launchUpdateHelper
// does (a copy of the installed 0.4.2 executable in the per-user temp dir) and
// performs the same readiness handshake on the ready file.
func (f *realHelperFixture) startHelper() *realHelperRun {
	f.t.Helper()
	helperPath := filepath.Join(f.tmp, "coslash-update-helper-realhelper.exe")
	f.copyFile(f.bins.legacy, helperPath)
	run := &realHelperRun{log: &realHelperLog{}, exited: make(chan struct{})}
	run.cmd = exec.Command(helperPath, "update-apply", f.target, f.staged, f.ready, realHelperAttemptID)
	run.cmd.Env = f.env()
	run.cmd.Dir = f.tmp
	run.cmd.Stdout, run.cmd.Stderr = run.log, run.log
	if err := run.cmd.Start(); err != nil {
		f.t.Fatalf("start real v0.4.2 helper: %v", err)
	}
	go func() {
		run.err = run.cmd.Wait()
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
	deadline := time.Now().Add(20 * time.Second)
	for {
		state, err := os.ReadFile(f.ready)
		if err == nil && strings.TrimSpace(string(state)) != "" {
			_ = os.Remove(f.ready)
			if strings.TrimSpace(string(state)) != "ready" {
				f.t.Fatalf("real helper could not claim the handoff (%q): %s", state, run.log)
			}
			return run
		}
		select {
		case <-run.exited:
			f.t.Fatalf("real helper exited before readiness (%v): %s", run.err, run.log)
		default:
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("real helper never wrote readiness: %s", run.log)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitSwap returns once the helper's MoveFileEx(staged -> target) has
// completed: the staged file is gone and the helper's backup exists. The
// helper creates the backup before the move and the move consumes the staged
// file, so this is a state barrier, not a sleep. Nothing here opens the target
// (a running target would block the helper's replace).
func (f *realHelperFixture) waitSwap(run *realHelperRun) time.Time {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, stagedErr := os.Lstat(f.staged)
		_, backupErr := os.Lstat(f.backup)
		if errors.Is(stagedErr, os.ErrNotExist) && backupErr == nil {
			return time.Now()
		}
		select {
		case <-run.exited:
			f.t.Fatalf("real helper finished before the swap was observed (%v): %s", run.err, run.log)
		default:
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("swap was not observed: %s", run.log)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *realHelperFixture) waitExit(run *realHelperRun) {
	f.t.Helper()
	select {
	case <-run.exited:
	case <-time.After(2 * time.Minute):
		f.t.Fatalf("real helper did not exit: %s", run.log)
	}
	if run.err != nil {
		f.t.Fatalf("real helper exited abnormally: %v: %s", run.err, run.log)
	}
}

func (f *realHelperFixture) outcome() string {
	body, err := os.ReadFile(filepath.Join(f.home, "update-results", realHelperAttemptID))
	if err != nil {
		return "(none)"
	}
	return string(body)
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
	if got, err := realHelperReadVersion(f.target); err != nil || got != wantVersion {
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

func TestRealLegacyHelper(t *testing.T) {
	bins := requireRealHelperBinaries(t)
	t.Logf("legacy helper sha256 %s (pinned official v0.4.2); new target sha256 %s", bins.legacySHA, bins.nextSHA)
	scenarios := []struct {
		name string
		run  func(*testing.T, realHelperBinaries)
	}{
		{"ReadyCollectorHelperRetiresItsOwnBackup", testRealHelperReady},
		{"RollbackWins", testRealHelperRollbackWins},
		{"SettlementWinsBeforeRollback", testRealHelperSettlementWins},
		{"SettlementHoldsBackupAcrossRealRollbackAttempt", testRealHelperHoldAcrossRestore},
		{"CollisionWindow1", testRealHelperCollisionWindow},
		{"CollisionWindow2", testRealHelperCollisionWindow},
		{"CollisionWindow3", testRealHelperCollisionWindow},
	}
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			scenario.run(t, bins)
		})
	}
}

// The new collector becomes the runtime owner, so the real helper itself
// retires its backup. Settlement afterwards must be a clean no-op.
func testRealHelperReady(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	run := f.startHelper()
	f.waitExit(run)
	if got := f.outcome(); got != "update_ready" {
		t.Fatalf("helper outcome %q, want update_ready: %s", got, run.log)
	}
	if _, err := os.Stat(filepath.Join(f.home, runtimeFilename)); err != nil {
		t.Fatalf("new collector never published its runtime descriptor: %v: %s", err, run.log)
	}
	f.requireInstalled(true)
	if err := f.settle(realHelperReadVersion); err != nil {
		t.Fatalf("settlement after the helper kept the update: %v", err)
	}
	f.requireClean()
}

// The helper reaches its readiness timeout and rolls back. Settlement must
// then report the changed installation and leave the restored 0.4.2 intact.
func testRealHelperRollbackWins(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	f.holdRuntimeLock()
	run := f.startHelper()
	f.waitExit(run)
	if got := f.outcome(); got != "update_failed" {
		t.Fatalf("helper outcome %q, want update_failed: %s", got, run.log)
	}
	f.requireInstalled(false)
	if err := f.settle(realHelperReadVersion); !errors.Is(err, errLegacyInstalledChanged) {
		t.Fatalf("settlement after real rollback returned %v, want errLegacyInstalledChanged", err)
	}
	f.requireInstalled(false)
	f.requireClean()
}

// Settlement retires the backup right after the real swap, long before the
// helper's rollback attempt. The helper's later restore must find nothing and
// leave the new target in place.
func testRealHelperSettlementWins(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	f.holdRuntimeLock()
	run := f.startHelper()
	f.waitSwap(run)
	if err := f.settle(realHelperReadVersion); err != nil {
		t.Fatalf("settlement after swap: %v", err)
	}
	f.waitExit(run)
	if !strings.Contains(run.log.String(), "restore previous coSlash Local after readiness timeout") {
		t.Fatalf("real helper did not attempt its rollback after settlement: %s", run.log)
	}
	t.Logf("helper outcome file after settlement won: %s", f.outcome())
	f.requireInstalled(true)
	f.requireClean()
}

// Deterministic barrier: settlement's version check under the backup hold does
// not return until the real helper process has exited, so the helper's one
// rollback attempt is guaranteed to happen while the hold is active. The hold
// must make it fail, and the installation must stay at the new version.
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
		return realHelperReadVersion(name)
	}
	if err := f.settle(read); err != nil {
		t.Fatalf("settlement holding the backup across the helper's rollback: %v; helper output: %s", err, run.log)
	}
	if calls != 3 {
		t.Fatalf("settlement read the version %d times, want 3 (before, under hold, after)", calls)
	}
	f.waitExit(run)
	if !strings.Contains(run.log.String(), "restore previous coSlash Local after readiness timeout") {
		t.Fatalf("real helper's rollback attempt was not refused while the backup was held: %s", run.log)
	}
	f.requireInstalled(true)
	f.requireClean()
}

// Probabilistic collision test. Attempts are positioned around the helper's
// documented rollback time and repeated until one is terminal. Whatever the
// interleaving (settled first, rollback during the hold, or rollback first),
// the installation must exist, match one known binary byte for byte, and agree
// with the settlement result. This does not pin an interleaving; it rejects
// the disappearing-target outcome.
func testRealHelperCollisionWindow(t *testing.T, bins realHelperBinaries) {
	f := newRealHelperFixture(t, bins)
	f.holdRuntimeLock()
	run := f.startHelper()
	swapped := f.waitSwap(run)
	positioned := swapped.Add(realHelperRestoreDelay - 5*time.Second)
	select {
	case <-time.After(time.Until(positioned)):
	case <-run.exited:
	}
	var result error
	attempts, afterExit := 0, 0
	for {
		attempts++
		result = f.settle(realHelperReadVersion)
		if result == nil || errors.Is(result, errLegacyInstalledChanged) {
			break
		}
		select {
		case <-run.exited:
			if afterExit++; afterExit > 3 {
				t.Fatalf("settlement never became terminal after the helper exited: %v; helper output: %s", result, run.log)
			}
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	f.waitExit(run)
	label := "settled"
	if result != nil {
		label = "rollback-won"
	}
	t.Logf("collision window: %s after %d attempts; helper output: %s", label, attempts, run.log)
	f.requireInstalled(result == nil)
	f.requireClean()
}

func realHelperKillProcessesUnder(dirs ...string) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if windows.Process32First(snapshot, &entry) != nil {
		return
	}
	for {
		if entry.ProcessID != 0 {
			realHelperKillIfUnder(entry.ProcessID, dirs)
		}
		if windows.Process32Next(snapshot, &entry) != nil {
			return
		}
	}
}

// Only a process whose image lives inside one of the owned fixture directories
// is ever terminated.
func realHelperKillIfUnder(pid uint32, dirs []string) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 4096)
	size := uint32(len(buffer))
	if windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size) != nil {
		return
	}
	image := strings.ToLower(windows.UTF16ToString(buffer[:size]))
	for _, dir := range dirs {
		if dir != "" && strings.HasPrefix(image, strings.ToLower(dir)+`\`) {
			_ = windows.TerminateProcess(handle, 1)
			_, _ = windows.WaitForSingleObject(handle, 5000)
			return
		}
	}
}
