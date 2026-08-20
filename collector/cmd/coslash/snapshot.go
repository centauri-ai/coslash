package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func runSnapshot(stdout, stderr io.Writer, args []string) int {
	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags.SetOutput(stderr)
	probe := flags.Bool("probe", false, "print framed host/capability facts without scanning transcripts")
	since := flags.String("since", "", "Mac-clock lower bound as epoch milliseconds; 0 means bounded full history")
	requestNow := flags.String("request-now", "", "Mac-clock request time as epoch milliseconds")
	agents := flags.String("agents", "", "exact agent allow-list; must be claude,codex")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "snapshot: unexpected argument %q\n", flags.Arg(0))
		return 2
	}

	logOutput := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(logOutput)

	if *probe {
		if *since != "" || *requestNow != "" || *agents != "" {
			fmt.Fprintln(stderr, "snapshot: --probe does not accept --since, --request-now, or --agents")
			return 2
		}
		return writeProbe(stdout, stderr)
	}
	if *since == "" || *requestNow == "" || *agents == "" {
		fmt.Fprintln(stderr, "usage: coslash snapshot --since <macEpochMs> --request-now <macEpochMs> --agents claude,codex")
		fmt.Fprintln(stderr, "       coslash snapshot --probe")
		return 2
	}
	sinceMs, err := parseEpochMillis("since", *since)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: %v\n", err)
		return 2
	}
	requestNowMs, err := parseEpochMillis("request-now", *requestNow)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: %v\n", err)
		return 2
	}
	if sinceMs > requestNowMs {
		fmt.Fprintln(stderr, "snapshot: --since must not exceed --request-now")
		return 2
	}
	if *agents != "claude,codex" {
		fmt.Fprintln(stderr, "snapshot: --agents must be exactly claude,codex")
		return 2
	}
	return writeSnapshot(stdout, stderr, sinceMs, requestNowMs)
}

func writeProbe(stdout, stderr io.Writer) int {
	probe, err := collector.BuildRemoteProbe(hostRemoteOptions(time.Now().UnixMilli(), 0))
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: build probe: %v\n", err)
		return 1
	}
	payload, err := remoteviewv1.MarshalProbe(probe)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: encode probe: %v\n", err)
		return 1
	}
	return writeFramed(stdout, stderr, payload)
}

func writeSnapshot(stdout, stderr io.Writer, sinceMs, requestNowMs int64) int {
	hostNow := time.Now().UnixMilli()
	cutoff := remoteviewv1.LinuxCutoffMs(hostNow, sinceMs, requestNowMs)
	result, err := collector.ListAgents(cutoff, []string{vendors.AgentClaude, vendors.AgentCodex}, remoteviewv1.MaxSessionsPerAgent)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: collect: %v\n", err)
		return 1
	}
	options := hostRemoteOptions(hostNow, time.Now().UnixMilli())
	options.RequestedSinceMs = sinceMs
	options.RequestNowMs = requestNowMs
	options.Truncated = result.Truncated
	if result.Truncated {
		options.TruncationReason = remoteviewv1.TruncationReasonSession
	}
	view, err := collector.BuildRemoteView(result.Sessions, options)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: build view: %v\n", err)
		return 1
	}
	payload, err := remoteviewv1.Marshal(view)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: encode view: %v\n", err)
		return 1
	}
	return writeFramed(stdout, stderr, payload)
}

func hostRemoteOptions(hostNowMs, collectedAtMs int64) collector.RemoteViewOptions {
	return collector.RemoteViewOptions{
		CollectorVersion: version,
		Capabilities:     []string{remoteviewv1.CapabilityRemoteView, remoteviewv1.CapabilityRemoteLaunch},
		LaunchableAgents: launchableAgents(),
		HostNowMs:        hostNowMs,
		CollectedAtMs:    collectedAtMs,
		HostOS:           runtime.GOOS,
		HostArch:         runtime.GOARCH,
	}
}

func writeFramed(stdout, stderr io.Writer, payload []byte) int {
	framed, err := remoteviewv1.EncodeFrame(payload)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: frame payload: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(framed); err != nil {
		fmt.Fprintf(stderr, "snapshot: write stdout: %v\n", err)
		return 1
	}
	return 0
}

func parseEpochMillis(name, raw string) (int64, error) {
	if raw == "" || raw[0] == '+' || raw[0] == '-' || (len(raw) > 1 && raw[0] == '0') {
		return 0, fmt.Errorf("--%s must be a non-negative base-10 integer", name)
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("--%s must be a non-negative base-10 integer", name)
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("--%s must be a non-negative base-10 integer", name)
	}
	return value, nil
}

func launchableAgents() []string {
	agents := make([]string, 0, 2)
	for _, agent := range []string{vendors.AgentClaude, vendors.AgentCodex} {
		if _, err := exec.LookPath(agent); err == nil {
			agents = append(agents, agent)
		}
	}
	return agents
}

func snapshotDispatched(args []string) bool {
	return len(args) > 1 && args[1] == "snapshot"
}

func rejectMixedServerFlags(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			return nil
		}
		if !strings.HasPrefix(arg, "-") || arg == "-h" || arg == "--help" {
			continue
		}
		if isSubcommandFlag(arg) {
			continue
		}
		return fmt.Errorf("subcommand does not accept server flag %q", arg)
	}
	return nil
}

func isSubcommandFlag(arg string) bool {
	switch {
	case arg == "--probe",
		strings.HasPrefix(arg, "--since"),
		strings.HasPrefix(arg, "--request-now"),
		strings.HasPrefix(arg, "--agents"),
		strings.HasPrefix(arg, "--agent"),
		strings.HasPrefix(arg, "--session"),
		strings.HasPrefix(arg, "--mode"),
		strings.HasPrefix(arg, "--handoff"):
		return true
	default:
		return false
	}
}

func runSnapshotMain(args []string) {
	if err := rejectMixedServerFlags(args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "coslash: %v\n", err)
		os.Exit(2)
	}
	os.Exit(runSnapshot(os.Stdout, os.Stderr, args[2:]))
}
