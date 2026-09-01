// Command coslash-helper answers one bounded collection request from a Mac
// coSlash instance driving it over SSH. It reads the SSH user's own Claude and
// Codex data, writes protocol records to stdout, and keeps no state: no daemon,
// no listener, no cache, and no transcript ever leaves the machine.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remotehelper"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// Exit codes are part of the transport contract: the Mac separates a helper that
// is missing or blocked (shell codes 126/127) from one that ran and reported
// what it could.
const (
	exitOK       = 0
	exitUsage    = 2
	exitPartial  = 3
	exitRequest  = 4
	exitInternal = 5
	exitResource = 6
)

// build is stamped at release time; it is diagnostic identity only, never a
// compatibility gate.
var build = "dev"

func main() {
	if len(os.Args) != 2 {
		usage()
		os.Exit(exitUsage)
	}
	switch os.Args[1] {
	case "version", "capabilities":
		os.Exit(runCapabilities(os.Stdout))
	case "collect":
		os.Exit(runCollect(context.Background(), os.Stdin, os.Stdout))
	default:
		usage()
		os.Exit(exitUsage)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: coslash-helper version|collect")
	fmt.Fprintln(os.Stderr, "collect reads one JSON request line on stdin.")
}

func runCapabilities(output io.Writer) int {
	document := remoteprotocol.Capabilities{
		Protocol:      remoteprotocol.VersionRange{Min: 1, Max: remoteprotocol.ProtocolVersion},
		Schema:        remoteprotocol.VersionRange{Min: 1, Max: remotefacts.SchemaVersion},
		ParserVersion: vendors.ParserVersion,
		Capabilities:  remotehelper.Capabilities,
		Build:         buildIdentity(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document); err != nil {
		fmt.Fprintln(os.Stderr, "coslash-helper: capabilities could not be written")
		return exitInternal
	}
	return exitOK
}

func runCollect(ctx context.Context, input io.Reader, output io.Writer) int {
	request, err := readRequest(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coslash-helper: %v\n", err)
		return exitRequest
	}
	outcome, err := remotehelper.Collect(ctx, request, remotehelper.Options{}, output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coslash-helper: collection stopped: %v\n", err)
		if errors.Is(err, remotehelper.ErrRecordLimit) {
			return exitResource
		}
		if outcome.Records > 0 {
			return exitPartial
		}
		return exitInternal
	}
	if !outcome.RequestComplete {
		fmt.Fprintln(os.Stderr, "coslash-helper: coverage is partial for this request")
		return exitPartial
	}
	return exitOK
}

// readRequest reads exactly one bounded request line. Nothing in the request is
// ever treated as a path, a command, or a filesystem name.
func readRequest(input io.Reader) (remoteprotocol.Request, error) {
	reader := bufio.NewReaderSize(
		io.LimitReader(input, remoteprotocol.MaxRequestBytes+1), 64<<10,
	)
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return remoteprotocol.Request{}, fmt.Errorf("read request: %w", err)
	}
	if len(line) == 0 {
		return remoteprotocol.Request{}, errors.New("request is empty")
	}
	if len(line) > remoteprotocol.MaxRequestBytes {
		return remoteprotocol.Request{}, errors.New("request exceeds byte limit")
	}
	if _, trailingErr := reader.ReadByte(); trailingErr == nil {
		return remoteprotocol.Request{}, errors.New("trailing content after request line")
	} else if !errors.Is(trailingErr, io.EOF) {
		return remoteprotocol.Request{}, fmt.Errorf("read request trailer: %w", trailingErr)
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var request remoteprotocol.Request
	if err := decoder.Decode(&request); err != nil {
		return remoteprotocol.Request{}, fmt.Errorf("decode request: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return remoteprotocol.Request{}, errors.New("trailing content after request")
	}
	if err := remoteprotocol.ValidateRequest(request); err != nil {
		return remoteprotocol.Request{}, fmt.Errorf("invalid request: %w", err)
	}
	return request, nil
}

func buildIdentity() string {
	if build != "dev" {
		return build
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return build
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return setting.Value
		}
	}
	return build
}
