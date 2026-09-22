package main

import (
	"flag"
	"fmt"
	"os"

	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: verify <bundle-directory>")
		os.Exit(2)
	}
	manifest, err := sessionbackupv1.VerifyDirectory(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup verification failed")
		os.Exit(1)
	}
	fmt.Printf("verified %s %d artifacts %d bytes\n", manifest.CompleteBackupSHA256, manifest.Summary.ArtifactCount, manifest.Summary.TotalBytes)
}
