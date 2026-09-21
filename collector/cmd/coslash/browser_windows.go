package main

import "os/exec"

func openBrowserNative(rawURL string) error {
	name, args := windowsBrowserCommand(rawURL)
	return exec.Command(name, args...).Run()
}
