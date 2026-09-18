//go:build !darwin && !windows

package launch

import (
	"fmt"
	"runtime"
)

func openTerminal(string, string, string) error {
	return fmt.Errorf("launch: opening a terminal is not supported on %s", runtime.GOOS)
}

func Available(string) bool {
	return false
}
