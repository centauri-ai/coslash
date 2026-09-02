package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/centauri-ai/coslash/collector/internal/remotemetrics"
)

func main() {
	manifestPath := flag.String("manifest", "", "privacy-safe measurement manifest")
	flag.Parse()
	if *manifestPath == "" {
		fmt.Fprintln(os.Stderr, "--manifest is required")
		os.Exit(2)
	}
	file, err := os.Open(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open measurement manifest failed")
		os.Exit(1)
	}
	defer file.Close()
	manifest, err := remotemetrics.Read(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid measurement manifest: %v\n", err)
		os.Exit(1)
	}
	totals, err := remotemetrics.Summarize(manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "measurement failed: %v\n", err)
		os.Exit(1)
	}
	output := struct {
		Environment any                  `json:"environment"`
		Totals      remotemetrics.Totals `json:"totals"`
	}{Environment: struct {
		Build      string  `json:"build"`
		Hardware   string  `json:"hardware"`
		Filesystem string  `json:"filesystem"`
		SSHRTTMs   float64 `json:"ssh_rtt_ms"`
	}{manifest.Build, manifest.Hardware, manifest.Filesystem, manifest.SSHRTTMs}, Totals: totals}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if encoder.Encode(output) != nil {
		os.Exit(1)
	}
}
