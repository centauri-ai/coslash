//go:build windows && realhelper

package main

// Native Windows paired first-upgrade proof: the REAL published v0.4.2 helper,
// a REAL new collector process and its own Hub-delivered legacy-settlement
// loop (watchAutomaticUpdates -> settleLegacyInstallation), against a loopback
// fake Hub. Settlement code never runs in the test process here: the only way
// the Hub can see a `done` or `update_failed` result is the new collector
// producing it. Driven by collector/scripts/windows-real-helper.ps1 -Paired.
//
// Real: the pinned 0.4.2 helper and 0.4.2 collector; the new collector built
// from this checkout (queue, hubclient, update loop, retireLegacyBackup, the
// install-channel detector under an owned %LOCALAPPDATA%); Windows file locks.
// Simulated: the Hub (a Go loopback server shaped from hubclient's own types;
// it is NOT the real Hub, whose legacy lifecycle is covered by the Hub
// repository's own integration test on Linux), the device identity and
// credential (synthetic), and, in the rollback test, the restore itself (see
// there for why the real helper cannot be the one restoring).
//
// Isolation. Every child gets the owned environment of the real-helper tests
// plus COSLASH_HUB_URL (loopback) and a closed-port proxy for any other HTTP(S).
// ONE thing is outside the owned tree: Windows has no file-based device
// credential store, so the collector reads its credential from Windows
// Credential Manager. The test writes one synthetic credential under a unique
// target (service ai.coslash.hub-device, account sha256(owned home):origin),
// refuses to overwrite an existing one, deletes it in cleanup and checks it is
// gone. That is a write to the current account's credential vault, so the test
// refuses to run unless the workflow declares an ephemeral CI runner.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

const (
	pairedRunnerEnv   = "COSLASH_REAL_HELPER_PAIRED"
	pairedRunnerValue = "ephemeral-ci-runner-credential-manager"
)

func requirePairedRunner(t *testing.T) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv(pairedRunnerEnv) != pairedRunnerValue {
		t.Fatalf("the paired proof writes one synthetic credential to Windows Credential Manager; it runs only in the CI job that sets GITHUB_ACTIONS=true and %s=%s on an ephemeral runner", pairedRunnerEnv, pairedRunnerValue)
	}
}

// pairedDiagnostics is logged when a paired subtest fails: every request the
// Hub saw, the collector's queue file (synthetic identity only; the credential
// is in Credential Manager and never printed) and the helper/collector logs.
func (f *realHelperFixture) pairedDiagnostics(hub *pairedHub, run func() *realHelperRun) {
	f.t.Cleanup(func() {
		if !f.t.Failed() {
			return
		}
		queue, _ := os.ReadFile(filepath.Join(f.home, "sync-v4", "queue.json"))
		log := "(helper not started)"
		if r := run(); r != nil {
			log = r.log()
		}
		background, _ := os.ReadFile(filepath.Join(f.home, "logs", backgroundLogName))
		f.t.Logf("paired diagnostics\n%s\nqueue.json: %s\nhelper log:\n%s\ncollector background log:\n%s", hub.dump(), queue, log, background)
	})
}

func pairedWaitFor(t *testing.T, what string, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type pairedIdentity struct {
	installID string
	store     hubclient.CredentialStore
}

// pair gives the fixture a synthetic paired identity: a v4 queue carrying an
// install ID and the binding of the fake Hub and credential, and the credential
// itself in Windows Credential Manager through the product's own store.
func (f *realHelperFixture) pair(hub *pairedHub) pairedIdentity {
	f.t.Helper()
	f.hubURL = hub.url
	f.t.Setenv("COSLASH_HOME", f.home)
	store := deviceCredentialStore("windows", hub.url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if existing, err := store.Load(ctx); !errors.Is(err, hubclient.ErrNotPaired) {
		f.t.Fatalf("refusing to touch a pre-existing credential for the unique owned target (value present: %v, error: %v)", existing != "", err)
	}
	f.t.Cleanup(func() {
		realHelperProcessesUnder([]string{f.install, f.tmp}, true)
		deleteContext, cancelDelete := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelDelete()
		if err := store.Delete(deleteContext); err != nil {
			f.t.Errorf("could not delete the synthetic Credential Manager entry: %v", err)
		}
		if _, err := store.Load(deleteContext); !errors.Is(err, hubclient.ErrNotPaired) {
			f.t.Errorf("synthetic Credential Manager entry is still present after cleanup: %v", err)
		}
	})
	if err := store.Save(ctx, hub.token); err != nil {
		f.t.Fatalf("save synthetic credential: %v", err)
	}
	client, err := hubClientForURL(realHelperNewVersion, hub.url)
	if err != nil {
		f.t.Fatal(err)
	}
	binding, err := client.V4Binding(ctx)
	if err != nil {
		f.t.Fatalf("compute queue binding: %v", err)
	}
	identity := pairedIdentity{installID: uuid.NewString(), store: store}
	queue, err := json.Marshal(map[string]any{"version": 1, "installId": identity.installID, "binding": binding, "entries": []any{}})
	if err != nil {
		f.t.Fatal(err)
	}
	dir := filepath.Join(f.home, "sync-v4")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "queue.json"), queue, 0o600); err != nil {
		f.t.Fatal(err)
	}
	return identity
}

type pairedQueueView struct {
	InstallID string          `json:"installId"`
	Binding   string          `json:"binding"`
	Update    json.RawMessage `json:"updateAttempt"`
}

func (f *realHelperFixture) readQueue() (pairedQueueView, bool) {
	var view pairedQueueView
	body, err := os.ReadFile(filepath.Join(f.home, "sync-v4", "queue.json"))
	return view, err == nil && json.Unmarshal(body, &view) == nil
}

// requireIdentityKept: the same install ID, the same binding (so the same Hub
// and credential) and the same credential value, after the update lifecycle.
func (f *realHelperFixture) requireIdentityKept(identity pairedIdentity, hub *pairedHub) {
	f.t.Helper()
	view, ok := f.readQueue()
	if !ok || view.InstallID != identity.installID {
		f.t.Fatalf("queue identity changed or unreadable: %+v (readable %v), want install ID %s", view, ok, identity.installID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if got, err := identity.store.Load(ctx); err != nil || got != hub.token {
		f.t.Fatalf("device credential changed or was lost: %v", err)
	}
}

func TestRealLegacyHelperPaired(t *testing.T) {
	requirePairedRunner(t)
	bins := requireRealHelperBinaries(t)
	t.Run("SuccessSettlesInTheCollectorsOwnLoop", func(t *testing.T) { testPairedSuccess(t, bins) })
	t.Run("RollbackIsDetectedAndReportedByTheCollectorsOwnLoop", func(t *testing.T) { testPairedRollbackReport(t, bins) })
}

// Real helper swaps in the new collector; the real new collector becomes the
// runtime owner, checks in, accepts the Hub's legacy-settlement command, settles
// in its own loop and the Hub receives its `done`. The helper normally retires
// the backup first (it sees the owner within 200 ms, long before a check-in
// round trip), so the loop usually verifies an already-retired backup here; the
// contended retire-under-hold path is exercised by the in-process scenarios.
func testPairedSuccess(t *testing.T, bins realHelperBinaries) {
	hub := newPairedHub(t, realHelperNewVersion, realHelperLegacyVersion, false)
	f := newRealHelperFixture(t, bins)
	identity := f.pair(hub)
	var run *realHelperRun
	f.pairedDiagnostics(hub, func() *realHelperRun { return run })
	run = f.startHelper(false)
	f.waitExit(run)
	f.requireHelperSilent(run)
	pairedWaitFor(t, "the new collector's own loop to report the legacy settlement done", 90*time.Second, func() bool {
		state, _, _, _ := hub.snapshot()
		return state == "verified"
	})
	if !f.ownerActive() {
		t.Fatalf("the new collector is not the runtime owner after settlement: %s", run.log())
	}
	f.requireInstalled(true)
	pairedWaitFor(t, "the collector to clear the completed update attempt", 60*time.Second, func() bool {
		view, ok := f.readQueue()
		return ok && len(bytes.TrimSpace(view.Update)) == 0
	})
	f.requireIdentityKept(identity, hub)
	_, checkIns, reports, rejected := hub.snapshot()
	sawNew := false
	for _, checkIn := range checkIns {
		if checkIn.version == realHelperNewVersion && checkIn.channel == "windows-script" {
			sawNew = true
		}
		if checkIn.version == realHelperLegacyVersion {
			t.Fatalf("the Hub heard from a 0.4.2 collector in the success path: %+v", checkIns)
		}
	}
	if !sawNew {
		t.Fatalf("the Hub never saw a windows-script check-in from the new collector: %+v", checkIns)
	}
	for _, report := range reports {
		if report.Result == "failed" {
			t.Fatalf("the collector reported a failure in the success path: %+v", report)
		}
	}
	// A failed result carried by a check-in, e.g. update_unavailable for a
	// redelivery the collector refused, is as much a failure as a report.
	if failures := hub.allFailures(); len(failures) != 0 {
		t.Fatalf("the Hub recorded failed results in the success path: %+v", failures)
	}
	t.Logf("hub check-in versions %v; reports %+v; unexpected requests %v", hub.checkInVersions(), reports, rejected)
	realHelperProcessesUnder([]string{f.install, f.tmp}, true)
	f.waitNoProcesses()
	f.requireClean()
}

// The real helper's own restore cannot be what the new collector's loop sees:
// the helper restores only when no runtime owner is active, and a collector that
// is running its settlement loop IS the owner (the loop starts after it takes
// runtime.ready). Windows also refuses to overwrite a running image, which the
// real-helper scenarios do not characterize. So the restore is SIMULATED the one
// way Windows allows over a running image: the running installed file is renamed
// aside and a copy of the pinned 0.4.2 binary takes its name, which is the state
// a successful rollback leaves. The Hub withholds the command until then so the
// loop is idle and the outcome is not a race. What this test proves is the new
// collector's reaction: its own loop reads the installed version, sees 0.4.2,
// marks the attempt failed, reports update_failed to the Hub, never reports
// done, leaves the restored bytes alone and restarts the installed 0.4.2, which
// checks in with the preserved identity and credential.
func testPairedRollbackReport(t *testing.T, bins realHelperBinaries) {
	hub := newPairedHub(t, realHelperNewVersion, realHelperLegacyVersion, true)
	f := newRealHelperFixture(t, bins)
	identity := f.pair(hub)
	var run *realHelperRun
	f.pairedDiagnostics(hub, func() *realHelperRun { return run })
	run = f.startHelper(false)
	f.waitExit(run)
	f.requireHelperSilent(run)
	pairedWaitFor(t, "the new collector to check in while the Hub withholds the update", 60*time.Second, func() bool {
		return len(hub.checkInVersions()) > 0
	})
	if !f.ownerActive() {
		t.Fatalf("the new collector is not the runtime owner: %s", run.log())
	}
	f.requireInstalled(true)
	aside := f.target + ".rolled-back-new"
	from, err := windows.UTF16PtrFromString(f.target)
	if err != nil {
		t.Fatal(err)
	}
	to, err := windows.UTF16PtrFromString(aside)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.MoveFileEx(from, to, 0); err != nil {
		t.Fatalf("cannot rename the running installed collector aside (the simulation needs this): %v", err)
	}
	f.copyFile(bins.legacy, f.target)
	f.requireInstalled(false)
	before := len(hub.checkInVersions())
	hub.release()
	pairedWaitFor(t, "the new collector's loop to report the rollback", 90*time.Second, func() bool {
		return len(hub.allFailures()) > 0
	})
	// Every failed result must be the new collector's update_failed. A failure
	// from a 0.4.2 collector would mean the Hub commanded it, which the real
	// Hub never does for a client older than the target.
	for _, failure := range hub.allFailures() {
		if failure.code != "update_failed" || failure.version == realHelperLegacyVersion {
			t.Fatalf("rollback was reported as %+v, want only update_failed from the new collector\n%s", failure, hub.dump())
		}
	}
	pairedWaitFor(t, "the restored 0.4.2 collector to check in", 120*time.Second, func() bool {
		versions := hub.checkInVersions()
		for _, version := range versions[before:] {
			if version == realHelperLegacyVersion {
				return true
			}
		}
		return false
	})
	state, checkIns, _, rejected := hub.snapshot()
	if state == "verified" {
		t.Fatalf("the Hub was told the update was done although the installation was rolled back: %+v", checkIns)
	}
	if got := hub.deliveriesTo(realHelperLegacyVersion); len(got) != 0 {
		t.Fatalf("the Hub commanded the restored 0.4.2 collector: %+v", got)
	}
	for _, failure := range hub.allFailures() {
		if failure.code != "update_failed" || failure.version == realHelperLegacyVersion {
			t.Fatalf("unexpected failure after the restore: %+v\n%s", failure, hub.dump())
		}
	}
	f.requireInstalled(false)
	f.requireIdentityKept(identity, hub)
	t.Logf("hub check-in versions %v; unexpected requests %v", hub.checkInVersions(), rejected)
	realHelperProcessesUnder([]string{f.install, f.tmp}, true)
	f.waitNoProcesses()
	if err := os.Remove(aside); err != nil {
		t.Fatalf("remove the renamed new collector image: %v", err)
	}
	f.requireClean()
}
