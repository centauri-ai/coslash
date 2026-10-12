package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type doctorHubClient interface {
	V4Binding(context.Context) (string, error)
}

func runDoctor(stdout, stderr io.Writer, args []string) int {
	return runDoctorCommand(stdout, stderr, args, "doctor")
}

func runStatus(stdout, stderr io.Writer, args []string) int {
	return runDoctorCommand(stdout, stderr, args, "status")
}

func runDoctorCommand(stdout, stderr io.Writer, args []string, command string) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print the diagnostics snapshot as JSON")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "usage: coslash %s [--json]\n", command)
		return 2
	}
	logOutput := log.Writer()
	log.SetOutput(io.Discard)
	ctx := context.Background()
	snapshot := diagnostics.CollectWithHub(ctx, version, true, collectDoctorHub(ctx))
	log.SetOutput(logOutput)
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(snapshot); err != nil {
			fmt.Fprintf(stderr, "write diagnostics: %v\n", err)
			return 2
		}
	} else {
		renderDoctor(stdout, snapshot)
	}
	return doctorExitCode(snapshot)
}

func collectDoctorHub(ctx context.Context) *diagnostics.HubSync {
	client, clientErr := hubClientFromEnvironment(version)
	origin := ""
	deviceName, _ := os.Hostname()
	deviceName = strings.TrimSpace(deviceName)
	if deviceName == "" {
		deviceName = "coSlash Local"
	}
	if client != nil && client.BaseURL != nil {
		origin = client.BaseURL.String()
		if name := strings.TrimSpace(client.DeviceName); name != "" {
			deviceName = name
		}
	} else if raw := strings.TrimSpace(os.Getenv("COSLASH_HUB_URL")); raw != "" {
		if baseURL, err := hubclient.ValidateHubURL(raw); err == nil {
			origin = baseURL.String()
		}
	} else if stored, err := readStoredHubURL(); err == nil {
		origin = stored
	}
	queue, queueErr := syncv4.ReadHealth("")
	if syncv4.IsMissingHealth(queueErr) {
		queueErr = nil
	}
	localPause := settings.Open().State().Config.SyncPaused || os.Getenv("COSLASH_SYNC_PAUSED") == "1"
	return buildDoctorHubHealth(ctx, client, origin, deviceName, localPause, hubclient.V4SyncEnabled(), queue, queueErr != nil || clientErr != nil)
}

func buildDoctorHubHealth(ctx context.Context, client doctorHubClient, origin, deviceName string, localPause, syncEnabled bool, queue syncv4.HealthSnapshot, unavailable bool) *diagnostics.HubSync {
	paired := false
	if client != nil {
		probeContext, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := client.V4Binding(probeContext)
		cancel()
		paired = err == nil
	}
	if queue.LastCheckInResult == "not_paired" || queue.LastCheckInResult == "hub:device_revoked" || queue.LastCheckInResult == "hub_rejected:device_revoked" {
		paired = false
	}
	state := "off"
	if paired && syncEnabled {
		switch {
		case localPause || queue.HubPaused:
			state = "paused"
		case queue.HubDeviceOff:
			state = "off"
		default:
			state = "on"
		}
	}
	lastResult := queue.LastCheckInResult
	if lastResult == "" {
		lastResult = "unknown"
	}
	if unavailable && lastResult == "unknown" {
		lastResult = "unavailable"
	}
	failures := make([]diagnostics.FailureCodeCount, 0, len(queue.TopFailureCodes))
	for _, failure := range queue.TopFailureCodes {
		failures = append(failures, diagnostics.FailureCodeCount{Code: failure.Code, Count: failure.Count})
	}
	var checkInAt *int64
	if queue.LastCheckInAt != nil {
		at := queue.LastCheckInAt.UnixMilli()
		checkInAt = &at
	}
	lastFailure, _ := hubFailureView(origin, queue.LastHubFailure, time.Now(), 0)
	return &diagnostics.HubSync{
		Origin: origin, DeviceName: deviceName, DeviceID: queue.InstallID, Paired: paired,
		LastCheckInAtMs: checkInAt, LastCheckInResult: lastResult, LastFailure: lastFailure, SyncState: state,
		PolicyKnown: queue.PolicyKnown, Pending: queue.Pending, Failing: queue.Failing,
		Parked: queue.Parked, TopFailureCodes: failures,
	}
}

func renderDoctor(w io.Writer, snapshot *diagnostics.Snapshot) {
	fmt.Fprintf(w, "coSlash %s diagnostics\n\nChecks\n", snapshot.Version)
	for _, check := range snapshot.Checks {
		marker := string(check.Status)
		if check.Status == diagnostics.StatusFail {
			marker = "FAIL"
		}
		fmt.Fprintf(w, "[%s] %s: %s\n", marker, check.Title, check.Detail)
		if check.Fix != "" {
			fmt.Fprintf(w, "       Fix: %s\n", check.Fix)
		}
	}
	fmt.Fprintln(w, "\nFacts")
	for _, source := range snapshot.Sources {
		cli := "not found"
		if source.CLI.Found {
			cli = source.CLI.Path
			if source.CLI.Version != "" {
				cli += " (" + source.CLI.Version + ")"
			}
		}
		fmt.Fprintf(w, "%s: %s, %d source entries, %d sessions; CLI %s\n", source.Label, source.Root, source.Entries, source.Sessions, cli)
		if source.IDE != nil {
			ide := "not found"
			if source.IDE.Found {
				ide = source.IDE.Path
				if source.IDE.Version != "" {
					ide += " (" + source.IDE.Version + ")"
				}
			}
			fmt.Fprintf(w, "  IDE %s\n", ide)
		}
	}
	fmt.Fprintf(w, "Storage: %s, writable=%t\n", snapshot.Storage.Home, snapshot.Storage.Writable)
	if snapshot.Hub != nil {
		fmt.Fprintln(w, "\nHub")
		origin := snapshot.Hub.Origin
		if origin == "" {
			origin = "not configured"
		}
		fmt.Fprintf(w, "Origin: %s\n", origin)
		device := snapshot.Hub.DeviceName
		if snapshot.Hub.DeviceID != "" {
			device += " (ID " + snapshot.Hub.DeviceID + ")"
		}
		paired := "no"
		if snapshot.Hub.Paired {
			paired = "yes"
		}
		fmt.Fprintf(w, "Device: %s\nPaired: %s\n", device, paired)
		if snapshot.Hub.LastCheckInResult == "not_paired" || snapshot.Hub.LastCheckInResult == "hub:device_revoked" || snapshot.Hub.LastCheckInResult == "hub_rejected:device_revoked" {
			fmt.Fprintln(w, "Connection: Not connected: removed from Hub; run coslash connect to add it again")
		}
		checkIn := "never"
		if snapshot.Hub.LastCheckInAtMs != nil {
			age := time.Since(time.UnixMilli(*snapshot.Hub.LastCheckInAtMs))
			if age < 0 {
				checkIn = "just now"
			} else {
				checkIn = age.Round(time.Second).String() + " ago"
			}
		}
		fmt.Fprintf(w, "Last check-in: %s (%s)\n", checkIn, snapshot.Hub.LastCheckInResult)
		if failure := snapshot.Hub.LastFailure; failure != nil {
			fmt.Fprintln(w, failure.Sentence)
			fmt.Fprintf(w, "Raw error: %s\n", failure.RawError)
		}
		fmt.Fprintf(w, "Sync: %s\nQueue: pending %d, failing %d, parked %d\n", snapshot.Hub.SyncState, snapshot.Hub.Pending, snapshot.Hub.Failing, snapshot.Hub.Parked)
		if len(snapshot.Hub.TopFailureCodes) > 0 {
			codes := make([]string, 0, len(snapshot.Hub.TopFailureCodes))
			for _, failure := range snapshot.Hub.TopFailureCodes {
				codes = append(codes, fmt.Sprintf("%s (%d)", failure.Code, failure.Count))
			}
			fmt.Fprintf(w, "Top failure codes: %s\n", strings.Join(codes, ", "))
		}
	}
}

func doctorExitCode(snapshot *diagnostics.Snapshot) int {
	for _, check := range snapshot.Checks {
		if check.Status == diagnostics.StatusFail {
			return 1
		}
	}
	return 0
}
