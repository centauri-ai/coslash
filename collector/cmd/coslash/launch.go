package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/launch"
)

func runLaunch(stdout, stderr io.Writer, args []string) int {
	flags := flag.NewFlagSet("launch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	agent := flags.String("agent", "", "claude or codex")
	sessionID := flags.String("session", "", "source session UUID")
	mode := flags.String("mode", "", "resume or new")
	handoffID := flags.String("handoff", "", "opaque handoff id for new mode")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "launch: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	if *agent == "" || *sessionID == "" || *mode == "" {
		fmt.Fprintln(stderr, "usage: coslash launch --agent <claude|codex> --session <uuid> --mode resume")
		fmt.Fprintln(stderr, "       coslash launch --agent <claude|codex> --session <uuid> --mode new --handoff <id>")
		return 2
	}
	if err := launch.ValidateRemoteAgent(*agent); err != nil {
		fmt.Fprintf(stderr, "launch: %v\n", err)
		return 2
	}
	if err := launch.ValidateUUIDSessionID(*sessionID); err != nil {
		fmt.Fprintf(stderr, "launch: %v\n", err)
		return 2
	}
	if err := launch.ValidateRemoteMode(*mode, *handoffID); err != nil {
		fmt.Fprintf(stderr, "launch: %v\n", err)
		return 2
	}

	found, err := collector.GetSessionFactsByAgent(*agent, *sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "launch: lookup session: %v\n", err)
		return 1
	}
	if found == nil {
		fmt.Fprintln(stderr, "launch: session not found")
		return 1
	}
	if err := launch.Execute(*agent, found.WorkingDirectory, *sessionID, *mode, *handoffID); err != nil {
		fmt.Fprintf(stderr, "launch: %v\n", err)
		if errors.Is(err, launch.ErrInvalidInput) {
			return 2
		}
		return 1
	}
	return 0
}

func launchDispatched(args []string) bool {
	return len(args) > 1 && args[1] == "launch"
}

func runLaunchMain(args []string) {
	if err := rejectMixedServerFlags(args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "coslash: %v\n", err)
		os.Exit(2)
	}
	os.Exit(runLaunch(os.Stdout, os.Stderr, args[2:]))
}
