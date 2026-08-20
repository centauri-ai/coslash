package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"

	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func runDoctor(stdout, stderr io.Writer, args []string) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print the diagnostics snapshot as JSON")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: coslash doctor [--json]")
		return 2
	}
	logOutput := log.Writer()
	log.SetOutput(io.Discard)
	snapshot := collectDoctorSnapshot(context.Background())
	log.SetOutput(logOutput)
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(snapshot); err != nil {
			fmt.Fprintf(stderr, "write diagnostics: %v\n", err)
			return 2
		}
	} else {
		fmt.Fprintln(stdout, diagnostics.FormatForCopy(snapshot))
	}
	return doctorExitCode(snapshot)
}

func collectDoctorSnapshot(ctx context.Context) *diagnostics.Snapshot {
	mgr := remote.NewManager(remote.Options{})
	defer mgr.Shutdown()

	state := settings.Open().State()
	if state.Valid {
		if err := mgr.LoadSettingsSnapshot(state.Config.Remote); err != nil {
			log.Printf("remote diagnostics: %v", err)
		}
	}
	return diagnostics.CollectWithRemote(ctx, version, true, remoteHealthFact(mgr))
}

func doctorExitCode(snapshot *diagnostics.Snapshot) int {
	for _, check := range snapshot.Checks {
		if check.Status == diagnostics.StatusFail {
			return 1
		}
	}
	return 0
}
