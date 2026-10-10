//go:build windows

package main

import "testing"

func TestWindowsBrowserCommand(t *testing.T) {
	name, args := windowsBrowserCommand("https://example.com/path?q=a&b=c")
	if name != "rundll32.exe" {
		t.Fatalf("command = %q, want %q", name, "rundll32.exe")
	}
	want := []string{"url.dll,FileProtocolHandler", "https://example.com/path?q=a&b=c"}
	if len(args) != len(want) {
		t.Fatalf("arguments = %q, want %q", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("argument %d = %q, want %q", i, args[i], want[i])
		}
	}
}
