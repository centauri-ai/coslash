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
	snapshot := diagnostics.Collect(context.Background(), version, true)
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
		fmt.Fprintf(w, "%s: %s, %d transcript files, %d session files; CLI %s\n", source.Label, source.Root, source.Transcripts, source.SessionFiles, cli)
	}
	fmt.Fprintf(w, "Storage: %s, writable=%t\n", snapshot.Storage.Home, snapshot.Storage.Writable)
}

func doctorExitCode(snapshot *diagnostics.Snapshot) int {
	for _, check := range snapshot.Checks {
		if check.Status == diagnostics.StatusFail {
			return 1
		}
	}
	return 0
}
