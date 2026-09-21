package main

import "fmt"

func openBrowserNative(string) error {
	return fmt.Errorf("opening a browser is not supported on linux")
}
