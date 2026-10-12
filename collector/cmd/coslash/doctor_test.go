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
	checkedAt := time.Now().Add(-2 * time.Minute)
	health := syncv4.HealthSnapshot{
		InstallID: "synthetic-device-id", PolicyKnown: true, HubPaused: true,
		LastCheckInAt: &checkedAt, LastCheckInResult: "hub_unreachable",
		Pending: 3, Failing: 2, Parked: 1,
		TopFailureCodes: []syncv4.FailureCodeCount{{Code: "too_large", Count: 2}},
	}
	hub := buildDoctorHubHealth(context.Background(), fakeDoctorHubClient{}, "https://hub.example.test", "synthetic-host", false, true, health, false)
	if !hub.Paired || hub.SyncState != "paused" || hub.DeviceID != "synthetic-device-id" || hub.Pending != 3 || hub.Failing != 2 || hub.Parked != 1 || hub.LastCheckInResult != "hub_unreachable" {
		t.Fatalf("Hub snapshot = %+v", hub)
	}
	var text bytes.Buffer
	renderDoctor(&text, &diagnostics.Snapshot{Version: "test", Hub: hub})
	for _, want := range []string{"Hub\n", "Origin: https://hub.example.test", "Paired: yes", "Sync: paused", "pending 3, failing 2, parked 1", "too_large (2)", "hub_unreachable"} {
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
}

func TestDoctorHubReportsUnpairedWhenBindingCannotLoad(t *testing.T) {
	hub := buildDoctorHubHealth(context.Background(), fakeDoctorHubClient{err: errors.New("not paired")}, "https://hub.example.test", "synthetic-host", false, true, syncv4.HealthSnapshot{}, false)
	if hub.Paired || hub.SyncState != "off" {
		t.Fatalf("Hub snapshot = %+v", hub)
	}
}

func TestDoctorHubTreatsRevokedDeviceAsUnpaired(t *testing.T) {
	queue := syncv4.HealthSnapshot{LastCheckInResult: "hub:device_revoked"}
	hub := buildDoctorHubHealth(context.Background(), fakeDoctorHubClient{}, "https://hub.example.test", "synthetic-host", false, true, queue, false)
	if hub.Paired || hub.SyncState != "off" {
		t.Fatalf("Hub snapshot = %+v", hub)
	}
}
