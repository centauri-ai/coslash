package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/centauri-ai/coslash/collector/internal/launch"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

type handoffPutResponse struct {
	ID string `json:"id"`
}

func runHandoff(stdout, stderr io.Writer, stdin io.Reader, args []string) int {
	if len(args) == 0 || args[0] != "put" {
		fmt.Fprintln(stderr, "usage: coslash handoff put --agent <claude|codex> --session <uuid>")
		return 2
	}
	flags := flag.NewFlagSet("handoff put", flag.ContinueOnError)
	flags.SetOutput(stderr)
	agent := flags.String("agent", "", "claude or codex")
	sessionID := flags.String("session", "", "source session UUID")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "handoff: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	if *agent == "" || *sessionID == "" {
		fmt.Fprintln(stderr, "usage: coslash handoff put --agent <claude|codex> --session <uuid>")
		return 2
	}
	id, err := launch.PutHandoff(*agent, *sessionID, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "handoff: %v\n", err)
		if errors.Is(err, launch.ErrInvalidInput) {
			return 2
		}
		return 1
	}
	payload, err := json.Marshal(handoffPutResponse{ID: id})
	if err != nil {
		fmt.Fprintf(stderr, "handoff: encode response: %v\n", err)
		return 1
	}
	framed, err := remoteviewv1.EncodeFrame(payload)
	if err != nil {
		fmt.Fprintf(stderr, "handoff: frame response: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(framed); err != nil {
		fmt.Fprintf(stderr, "handoff: write stdout: %v\n", err)
		return 1
	}
	return 0
}

func handoffDispatched(args []string) bool {
	return len(args) > 1 && args[1] == "handoff"
}

func runHandoffMain(args []string) {
	if err := rejectMixedServerFlags(args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "coslash: %v\n", err)
		os.Exit(2)
	}
	os.Exit(runHandoff(os.Stdout, os.Stderr, os.Stdin, args[2:]))
}
