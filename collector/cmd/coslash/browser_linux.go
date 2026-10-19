package main

import (
	"errors"
	"os"
	"os/exec"
)

func openBrowserNative(rawURL string) error {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("no graphical session is available to open a browser")
	}
	return exec.Command("xdg-open", rawURL).Run()
}
