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
	records := make([]fullsessionv1.Record, 0, flags.NArg())
	for _, path := range flags.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			fail()
		}
		record, err := fullsessionv1.Decode(data)
		if err != nil {
			fail()
		}
		records = append(records, record)
	}
	report, err := fullsessionv1.Measure(records, *version)
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
