package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
)

func main() {
	flags := flag.NewFlagSet("measure", flag.ExitOnError)
	version := flags.String("collector-version", "", "collector revision measured")
	flags.Parse(os.Args[1:])
	if *version == "" {
		fmt.Fprintln(os.Stderr, "--collector-version is required")
		os.Exit(2)
	}

	log.SetOutput(io.Discard)
	values, err := collector.List(0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "session collection failed")
		os.Exit(1)
	}
	corpus := make([]session.Session, len(values))
	roots := make([]string, len(values))
	for i, value := range values {
		corpus[i] = *value
		roots[i] = session.RepositoryRoot(value.WorkingDirectory)
		if roots[i] == "" {
			roots[i] = value.WorkingDirectory
		}
	}
	report, err := sessionexport.MeasureCorpus(corpus, func(i int) sessionexport.BuildOptions {
		return sessionexport.BuildOptions{CollectorVersion: *version, RepositoryRoot: roots[i]}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "measurement failed: %v\n", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		os.Exit(1)
	}
}
