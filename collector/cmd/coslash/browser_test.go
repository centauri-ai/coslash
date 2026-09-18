package main

import "testing"

func TestValidateBrowserURL(t *testing.T) {
	for _, test := range []struct {
		url     string
		wantErr bool
	}{
		{url: "http://127.0.0.1:8787/#t=secret"},
		{url: "https://example.com/path?q=value"},
		{url: "file:///tmp/coslash", wantErr: true},
		{url: "javascript:alert(1)", wantErr: true},
		{url: "//example.com/path", wantErr: true},
		{url: "not a URL", wantErr: true},
	} {
		t.Run(test.url, func(t *testing.T) {
			err := validateBrowserURL(test.url)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateBrowserURL(%q) error = %v, want error %v", test.url, err, test.wantErr)
			}
		})
	}
}

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
