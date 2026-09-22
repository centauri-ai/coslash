package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func main() {
	version := flag.String("collector-version", "", "collector revision measured")
	flag.Parse()
	if *version == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "--collector-version and one or more bundle directories are required")
		os.Exit(2)
	}
	measurer, err := sessionbackupv1.NewMeasurer(*version)
	if err != nil {
		fail()
	}
	for _, root := range flag.Args() {
		manifest, err := sessionbackupv1.VerifyDirectory(root)
		if err != nil || measurer.Add(manifest) != nil {
			fail()
		}
	}
	report, err := measurer.Report()
	if err != nil {
		fail()
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if encoder.Encode(report) != nil {
		os.Exit(1)
	}
}

func fail() {
	fmt.Fprintln(os.Stderr, "measurement failed")
	os.Exit(1)
}
