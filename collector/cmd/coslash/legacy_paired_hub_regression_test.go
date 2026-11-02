package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

// pairedHubProbe drives the fake with the product's own hubclient and
// syncv4.Queue, as the paired Windows test's collector does.
type pairedHubProbe struct {
	t     *testing.T
	hub   *pairedHub
	queue *syncv4.Queue
}

func newPairedHubProbe(t *testing.T, hub *pairedHub) *pairedHubProbe {
	t.Helper()
	t.Setenv("COSLASH_HOME", t.TempDir())
	store := deviceCredentialStore("linux", hub.url)
	if err := store.Save(context.Background(), hub.token); err != nil {
		t.Fatal(err)
	}
	queue, err := syncv4.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &pairedHubProbe{t, hub, queue}
}

// checkIn performs one check-in as the given collector version and returns the
// update commands the Hub delivered.
func (p *pairedHubProbe) checkIn(version string, results ...hubclient.V4CommandResult) []hubclient.V4Command {
	p.t.Helper()
	client, err := hubClientForURL(version, p.hub.url)
	if err != nil {
		p.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := client.V4CheckIn(ctx, hubclient.V4Queue{}, 0, results, nil, nil)
	if err != nil {
		p.t.Fatalf("check-in as %s: %v\n%s", version, err, p.hub.dump())
	}
	return out.Commands
}

func (p *pairedHubProbe) accept(command hubclient.V4Command, current string) error {
	return p.queue.AcceptUpdate(command, current, time.Now())
}

// The fake must hand the collector the same attempt, byte for byte, every time
// the real Hub would: the stored row is immutable. Before the fix every
// delivery carried a new expiresAt, so the second one was rejected.
func TestPairedHubRedeliversTheStoredAttemptUnchanged(t *testing.T) {
	hub := newPairedHub(t, "0.4.3", "0.4.2", false)
	probe := newPairedHubProbe(t, hub)
	var first hubclient.V4Command
	for delivery := 1; delivery <= 4; delivery++ {
		commands := probe.checkIn("0.4.3")
		if len(commands) != 1 || commands[0].Type != "update" || commands[0].ID != pairedAttemptID {
			t.Fatalf("delivery %d: want the one legacy-settlement update command, got %+v", delivery, commands)
		}
		if delivery == 1 {
			first = commands[0]
		} else if string(commands[0].Payload) != string(first.Payload) {
			t.Fatalf("delivery %d changed the stored attempt:\n first: %s\n  this: %s", delivery, first.Payload, commands[0].Payload)
		}
		if err := probe.accept(commands[0], "0.4.3"); err != nil {
			t.Fatalf("delivery %d: the collector's queue rejected the redelivered attempt: %v\n%s", delivery, err, hub.dump())
		}
		if pending := probe.queue.PendingUpdate(); pending == nil || !pending.LegacySettlement || pending.ID != pairedAttemptID {
			t.Fatalf("delivery %d: unexpected pending update %+v", delivery, pending)
		}
	}
	if failures := hub.allFailures(); len(failures) != 0 {
		t.Fatalf("no result was sent, yet the Hub recorded failures: %+v", failures)
	}
}

// Negative control: the original per-delivery expiry makes the product's own
// AcceptUpdate refuse the second delivery. This is the product behaving
// correctly against an impossible Hub, and it is why the success test timed out.
func TestPairedHubPerDeliveryExpiryIsRejectedByTheCollector(t *testing.T) {
	hub := newPairedHub(t, "0.4.3", "0.4.2", false)
	hub.freshExpiryPerDelivery = true
	probe := newPairedHubProbe(t, hub)
	if err := probe.accept(probe.checkIn("0.4.3")[0], "0.4.3"); err != nil {
		t.Fatalf("first delivery must be accepted: %v", err)
	}
	err := probe.accept(probe.checkIn("0.4.3")[0], "0.4.3")
	if err == nil || !strings.Contains(err.Error(), "another update is active") {
		t.Fatalf("a redelivery with a different expiresAt must be refused, got %v", err)
	}
}

// The real Hub never sends an update command to a collector older than the
// target. The original fake did, and 0.4.2 answered failed/command_failed.
func TestPairedHubNeverCommandsTheLegacyCollectorOrOneWithoutTheCapability(t *testing.T) {
	hub := newPairedHub(t, "0.4.3", "0.4.2", false)
	probe := newPairedHubProbe(t, hub)
	if commands := probe.checkIn("0.4.2"); len(commands) != 0 {
		t.Fatalf("the Hub delivered %+v to a 0.4.2 collector", commands)
	}
	if got := hub.deliveriesTo("0.4.2"); len(got) != 0 {
		t.Fatalf("deliveries to 0.4.2: %+v", got)
	}
	// Even after a failure report, a restored 0.4.2 is never commanded.
	probe.checkIn("0.4.3", hubclient.V4CommandResult{CommandID: pairedAttemptID, Result: "failed", Error: "update_failed"})
	if commands := probe.checkIn("0.4.2"); len(commands) != 0 {
		t.Fatalf("the Hub delivered %+v to a restored 0.4.2 collector", commands)
	}
}

// Legacy-attempt result handling: a failure keeps the attempt accepted and
// redelivered; only a clean done from the target version verifies it.
func TestPairedHubLegacyAttemptResultSemantics(t *testing.T) {
	hub := newPairedHub(t, "0.4.3", "0.4.2", false)
	probe := newPairedHubProbe(t, hub)
	probe.checkIn("0.4.3", hubclient.V4CommandResult{CommandID: pairedAttemptID, Result: "failed", Error: "update_failed"})
	if state, _, _, _ := hub.snapshot(); state != "accepted" {
		t.Fatalf("a failed result must not finish a legacy attempt, state %s", state)
	}
	if len(probe.checkIn("0.4.3")) != 1 {
		t.Fatal("an accepted legacy attempt keeps being delivered")
	}
	probe.checkIn("0.4.2", hubclient.V4CommandResult{CommandID: pairedAttemptID, Result: "done"})
	if state, _, _, _ := hub.snapshot(); state != "accepted" {
		t.Fatalf("a done from a collector older than the target must not verify, state %s", state)
	}
	probe.checkIn("0.4.3", hubclient.V4CommandResult{CommandID: pairedAttemptID, Result: "done", Error: "already_current"})
	if state, _, _, _ := hub.snapshot(); state != "accepted" {
		t.Fatalf("a done with a code must not verify, state %s", state)
	}
	probe.checkIn("0.4.3", hubclient.V4CommandResult{CommandID: pairedAttemptID, Result: "done"})
	if state, _, _, _ := hub.snapshot(); state != "verified" {
		t.Fatalf("a clean done from the target must verify, state %s", state)
	}
	if len(probe.checkIn("0.4.3")) != 0 {
		t.Fatal("a verified attempt is not delivered again")
	}
}
