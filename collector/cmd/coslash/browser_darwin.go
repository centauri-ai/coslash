package main

import "os/exec"

func openBrowserNative(rawURL string) error {
	return exec.Command("open", rawURL).Run()
}
