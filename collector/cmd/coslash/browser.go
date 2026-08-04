package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser opens url in the user's default browser. Like internal/launch,
// macOS is the only platform implemented.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	}
	return fmt.Errorf("opening a browser is not supported on %s", runtime.GOOS)
}
