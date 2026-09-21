package main

import (
	"fmt"
	"net/url"
	"strings"
)

// openBrowser opens rawURL in the user's default browser.
func openBrowser(rawURL string) error {
	if err := validateBrowserURL(rawURL); err != nil {
		return err
	}
	return openBrowserNative(rawURL)
}

func validateBrowserURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid browser URL %q: only absolute HTTP and HTTPS URLs are allowed", rawURL)
	}
	hostname := parsed.Hostname()
	if parsed.User != nil || hostname == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		strings.Contains(rawURL, `\`) || strings.HasPrefix(hostname, ".") ||
		strings.HasSuffix(hostname, ".") || strings.Contains(hostname, "..") {
		return fmt.Errorf("invalid browser URL %q: only absolute HTTP and HTTPS URLs are allowed", rawURL)
	}
	return nil
}

func windowsBrowserCommand(rawURL string) (string, []string) {
	return "rundll32.exe", []string{"url.dll,FileProtocolHandler", rawURL}
}
