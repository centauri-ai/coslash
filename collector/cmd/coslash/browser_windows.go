package main

import "os/exec"

func windowsBrowserCommand(rawURL string) (string, []string) {
	return "rundll32.exe", []string{"url.dll,FileProtocolHandler", rawURL}
}

func openBrowserNative(rawURL string) error {
	name, args := windowsBrowserCommand(rawURL)
	return exec.Command(name, args...).Run()
}
