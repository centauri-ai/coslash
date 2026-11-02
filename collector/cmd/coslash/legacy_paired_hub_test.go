package main

// Loopback fake Hub for the paired Windows first-upgrade proof, and its Linux
// regression tests. No build tags: the fake itself runs, and is checked,
// everywhere; only the Windows test that uses it is tagged.
//
// It models the REAL Hub's legacy-settlement delivery, which was read from the
// Hub source at coslash-server 9361910c (internal/devices/v4_updates.go,
// updateCheckIn and acknowledgeUpdateResults; v4_postgres.go for update-report):
//
//   - The attempt is one stored row. Its expires_at is fixed when the batch is
//     created and every delivery re-serializes that same value. The first
//     version of this fake stamped time.Now()+24h on each delivery, so a
//     redelivered command carried a different expiresAt, which Queue.AcceptUpdate
//     rejects ("another update is active"); the collector then answered
//     failed/update_unavailable for the active attempt.
//   - A legacy attempt is accepted by a 0.4.2 check-in (queued -> accepted) and
//     the Hub never sends a command to a client older than the target: for
//     comparison < 0 it does `continue`. The first fake delivered the update
//     command to the restored 0.4.2 collector, which does not know it and answers
//     failed/command_failed.
//   - The command is delivered only to a client at or past the target that
//     advertises legacy-update-settlement/v1, and keeps being delivered while the
//     attempt is accepted.
//   - A `failed` result on a legacy attempt only records the result code
//     (update_failed, or update_unavailable); the attempt stays accepted. A `done`
//     with an empty code from a client at or past the target verifies it.
//
// Not modeled: batches, the queued -> superseded rule for a new-version check-in
// on a never-accepted attempt (the attempt starts accepted, as if the real 0.4.2
// collector had accepted it before the swap), expiry, Postgres, auth beyond the
// device token, the catalog and policy generations.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const pairedAttemptID = "33333333-3333-4333-8333-333333333333"

type pairedCheckIn struct {
	version, channel string
	capabilities     []string
	results          []hubclient.V4CommandResult
}

type pairedDelivery struct {
	version, expiresAt string
}

type pairedFailure struct {
	code, version, via string
}

type pairedHub struct {
	server *httptest.Server
	url    string
	token  string

	target, legacy string
	// freshExpiryPerDelivery reproduces the original harness defect. It exists
	// only so a test can show that the defect is what the stable expiry fixes.
	freshExpiryPerDelivery bool

	mu         sync.Mutex
	started    time.Time
	withheld   bool
	state      string // "accepted" until a done check-in verifies it, then "verified"
	resultCode string
	expiresAt  time.Time
	checkIns   []pairedCheckIn
	deliveries []pairedDelivery
	failures   []pairedFailure
	reports    []hubclient.V4CommandResult
	rejected   []string
	requests   []string
}

func newPairedHub(t *testing.T, target, legacy string, withhold bool) *pairedHub {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	h := &pairedHub{
		token: "synthetic-device-credential-" + hex.EncodeToString(raw), target: target, legacy: legacy,
		withheld: withhold, state: "accepted", started: now,
		// Fixed once, with Postgres timestamptz precision, like the stored row.
		expiresAt: now.Add(24 * time.Hour).Truncate(time.Microsecond),
	}
	h.server = httptest.NewServer(h)
	h.url = h.server.URL
	t.Cleanup(h.server.Close)
	return h
}

func (h *pairedHub) respond(w http.ResponseWriter, value any) {
	body, _ := json.Marshal(value)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (h *pairedHub) note(format string, args ...any) {
	h.requests = append(h.requests, fmt.Sprintf("+%6.2fs ", time.Since(h.started).Seconds())+fmt.Sprintf(format, args...))
}

// atOrPastTarget: the client is the target version or newer.
func (h *pairedHub) atOrPastTarget(version string) bool {
	return !hubclient.NewerClientVersion(version, h.target)
}

func (h *pairedHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Device "+h.token {
		h.mu.Lock()
		h.rejected = append(h.rejected, "unauthorized "+r.Method+" "+r.URL.Path)
		h.note("401 %s %s", r.Method, r.URL.Path)
		h.mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v4/devices/me/wait":
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		h.respond(w, map[string]any{"configVersion": 1, "changed": false, "commandsAvailable": false, "syncRequested": false})
	case r.Method == http.MethodPost && r.URL.Path == "/v4/devices/me/check-in":
		var body struct {
			ClientVersion  string                      `json:"clientVersion"`
			InstallChannel string                      `json:"installChannel"`
			Capabilities   []string                    `json:"capabilities"`
			Results        []hubclient.V4CommandResult `json:"results"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		h.checkIns = append(h.checkIns, pairedCheckIn{body.ClientVersion, body.InstallChannel, body.Capabilities, body.Results})
		var summary []string
		for _, result := range body.Results {
			summary = append(summary, fmt.Sprintf("%s/%s", result.Result, result.Error))
			if result.CommandID != pairedAttemptID {
				continue
			}
			switch {
			case result.Result == "done" && result.Error == "" && h.atOrPastTarget(body.ClientVersion) && h.state == "accepted":
				h.state = "verified"
			case result.Result == "failed":
				h.noteFailure(result, body.ClientVersion, "check-in")
			}
		}
		commands := []any{}
		var delivered string
		if !h.withheld && h.state == "accepted" && h.atOrPastTarget(body.ClientVersion) && slices.Contains(body.Capabilities, "legacy-update-settlement/v1") {
			expires := h.expiresAt
			if h.freshExpiryPerDelivery {
				expires = time.Now().UTC().Add(24 * time.Hour)
			}
			payload := map[string]any{"attemptId": pairedAttemptID, "targetVersion": h.target, "expiresAt": expires, "legacySettlement": true}
			commands = append(commands, map[string]any{"id": pairedAttemptID, "type": "update", "payload": payload})
			delivered = expires.Format(time.RFC3339Nano)
			h.deliveries = append(h.deliveries, pairedDelivery{body.ClientVersion, delivered})
		}
		h.note("check-in %s channel=%s results=%v delivered=%q state=%s", body.ClientVersion, body.InstallChannel, summary, delivered, h.state)
		h.mu.Unlock()
		h.respond(w, map[string]any{
			"configVersion": 1,
			"config":        map[string]any{"paused": true, "autoUpdate": false, "deviceOff": true, "leaveOut": []string{}, "agentKnowledge": false},
			"commands":      commands, "minVersion": h.legacy, "recommendedVersion": h.target,
			"nextCheckInSeconds": 60, "capabilities": []string{"update-completion/v1"},
		})
	case r.Method == http.MethodPost && r.URL.Path == "/v4/devices/me/update-report":
		var result hubclient.V4CommandResult
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&result) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		h.reports = append(h.reports, result)
		if result.CommandID == pairedAttemptID && result.Result == "failed" {
			// update-report carries no client version; only the new collector's
			// update loop sends it.
			h.noteFailure(result, h.target, "update-report")
		}
		h.note("update-report %s/%s", result.Result, result.Error)
		active := h.state == "accepted"
		h.mu.Unlock()
		h.respond(w, map[string]any{"active": active})
	default:
		h.mu.Lock()
		h.rejected = append(h.rejected, r.Method+" "+r.URL.Path)
		h.note("404 %s %s", r.Method, r.URL.Path)
		h.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}
}

// noteFailure is called with h.mu held.
func (h *pairedHub) noteFailure(result hubclient.V4CommandResult, version, via string) {
	h.failures = append(h.failures, pairedFailure{result.Error, version, via})
	h.resultCode = "update_failed"
	if result.Error == "update_unavailable" {
		h.resultCode = "update_unavailable"
	}
}

func (h *pairedHub) release() {
	h.mu.Lock()
	h.withheld = false
	h.mu.Unlock()
}

func (h *pairedHub) snapshot() (state string, checkIns []pairedCheckIn, reports []hubclient.V4CommandResult, rejected []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state, slices.Clone(h.checkIns), slices.Clone(h.reports), slices.Clone(h.rejected)
}

func (h *pairedHub) checkInVersions() []string {
	_, checkIns, _, _ := h.snapshot()
	var versions []string
	for _, checkIn := range checkIns {
		versions = append(versions, checkIn.version)
	}
	return versions
}

func (h *pairedHub) allFailures() []pairedFailure {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.failures)
}

func (h *pairedHub) deliveriesTo(version string) []pairedDelivery {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []pairedDelivery
	for _, delivery := range h.deliveries {
		if delivery.version == version {
			out = append(out, delivery)
		}
	}
	return out
}

// dump is the failure diagnostic: every request the Hub saw, in order.
func (h *pairedHub) dump() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return fmt.Sprintf("fake Hub state=%s resultCode=%q withheld=%v expiresAt=%s deliveries=%d failures=%+v rejected=%v\n%s",
		h.state, h.resultCode, h.withheld, h.expiresAt.Format(time.RFC3339Nano), len(h.deliveries), h.failures, h.rejected, strings.Join(h.requests, "\n"))
}
