package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type fakeDoctorHubClient struct{ err error }

func (client fakeDoctorHubClient) V4Binding(context.Context) (string, error) {
	return "synthetic-binding", client.err
}

func TestDoctorHubSectionReportsPairingQueueAndLastCheckIn(t *testing.T) {
	previousLocation := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previousLocation })
	checkedAt := time.Date(2026, 10, 12, 8, 10, 0, 0, time.UTC)
	health := syncv4.HealthSnapshot{
		InstallID: "synthetic-device-id", PolicyKnown: true, HubPaused: true,
		LastCheckInAt: &checkedAt, LastCheckInResult: "hub_unreachable",
		LastHubFailure: &syncv4.HubContactFailure{
			Cause: "connect", SinceMs: time.Date(2026, 10, 12, 8, 12, 0, 0, time.UTC).UnixMilli(),
			Attempts: 14, RawError: "dial tcp: i/o timeout",
		},
		Pending: 3, Failing: 2, Parked: 1,
		TopFailureCodes: []syncv4.FailureCodeCount{{Code: "too_large", Count: 2}},
	}
	hub := buildDoctorHubHealth(context.Background(), fakeDoctorHubClient{}, "https://hub.example.test", "synthetic-host", false, true, health, false)
	if !hub.Paired || hub.SyncState != "paused" || hub.DeviceID != "synthetic-device-id" || hub.Pending != 3 || hub.Failing != 2 || hub.Parked != 1 || hub.LastCheckInResult != "hub_unreachable" {
		t.Fatalf("Hub snapshot = %+v", hub)
	}
	var text bytes.Buffer
	renderDoctor(&text, &diagnostics.Snapshot{Version: "test", Hub: hub})
	for _, want := range []string{"Hub\n", "Origin: https://hub.example.test", "Paired: yes", "Sync: paused", "pending 3, failing 2, parked 1", "too_large (2)", "hub_unreachable", "Hub: can't connect to hub.example.test since 08:12 (14 attempts) — Connection timed out. Check your network, VPN or proxy.", "Raw error: dial tcp: i/o timeout"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("doctor output omitted %q: %s", want, text.String())
		}
	}
	encoded, err := json.Marshal(hub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "title") || strings.Contains(string(encoded), "summary") {
		t.Fatalf("Hub health leaked session content: %s", encoded)
	}
	wantJSON := `{"origin":"https://hub.example.test","deviceName":"synthetic-host","deviceId":"synthetic-device-id","paired":true,"lastCheckInAtMs":1791792600000,"lastCheckInResult":"hub_unreachable","lastFailure":{"host":"hub.example.test","cause":"connect","sinceMs":1791792720000,"attempts":14,"sentence":"Hub: can't connect to hub.example.test since 08:12 (14 attempts) — Connection timed out. Check your network, VPN or proxy.","rawError":"dial tcp: i/o timeout"},"syncState":"paused","policyKnown":true,"pending":3,"failing":2,"parked":1,"topFailureCodes":[{"code":"too_large","count":2}]}`
	if string(encoded) != wantJSON {
		t.Fatalf("doctor Hub JSON = %s, want %s", encoded, wantJSON)
	}
}

func TestDoctorHubReportsUnpairedWhenBindingCannotLoad(t *testing.T) {
	hub := buildDoctorHubHealth(context.Background(), fakeDoctorHubClient{err: errors.New("not paired")}, "https://hub.example.test", "synthetic-host", false, true, syncv4.HealthSnapshot{}, false)
	if hub.Paired || hub.SyncState != "off" {
		t.Fatalf("Hub snapshot = %+v", hub)
	}
}

func TestDoctorHubTreatsRevokedDeviceAsUnpaired(t *testing.T) {
	queue := syncv4.HealthSnapshot{LastCheckInResult: "not_paired"}
	hub := buildDoctorHubHealth(context.Background(), fakeDoctorHubClient{}, "https://hub.example.test", "synthetic-host", false, true, queue, false)
	if hub.Paired || hub.SyncState != "off" || hub.LastCheckInResult != "not_paired" {
		t.Fatalf("Hub snapshot = %+v", hub)
	}
	var output bytes.Buffer
	renderDoctor(&output, &diagnostics.Snapshot{Version: "test", Hub: hub})
	if !strings.Contains(output.String(), "Not connected: removed from Hub; run coslash connect to add it again") {
		t.Fatalf("doctor omitted revoked-device recovery hint: %s", output.String())
	}
}
