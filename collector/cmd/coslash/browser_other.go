//go:build !darwin && !linux && !windows

package main

import (
	"fmt"
	"runtime"
)

func openBrowserNative(string) error {
	return fmt.Errorf("opening a browser is not supported on %s", runtime.GOOS)
}
