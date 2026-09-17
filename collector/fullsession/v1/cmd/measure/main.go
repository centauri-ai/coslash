package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

func main() {
	flags := flag.NewFlagSet("measure", flag.ExitOnError)
	version := flags.String("collector-version", "", "collector revision measured")
	flags.Parse(os.Args[1:])
	if *version == "" || flags.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "--collector-version and one or more record files are required")
		os.Exit(2)
	}
	measurer, err := fullsessionv1.NewMeasurer(*version)
	if err != nil {
		fail()
	}
	for _, path := range flags.Args() {
		file, err := os.Open(path)
		if err != nil {
			fail()
		}
		record, decodeErr := fullsessionv1.DecodeReader(file)
		closeErr := file.Close()
		if decodeErr != nil || closeErr != nil {
			fail()
		}
		if err := measurer.Add(record); err != nil {
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
