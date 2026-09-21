package main

import "testing"

func TestValidateBrowserURL(t *testing.T) {
	for _, test := range []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "loopback IPv4 with port", url: "http://127.0.0.1:8787/#t=secret"},
		{name: "loopback IPv6 with port", url: "https://[::1]:8787/path"},
		{name: "DNS host with port", url: "https://example.com:443/path?q=value"},
		{name: "localhost with port", url: "http://localhost:8787/"},
		{name: "userinfo", url: "https://user:password@example.com/", wantErr: true},
		{name: "leading host dot", url: "https://.example.com/", wantErr: true},
		{name: "trailing host dot", url: "https://example.com./", wantErr: true},
		{name: "empty host label", url: "https://example..com/", wantErr: true},
		{name: "backslash in host", url: `https://example.com\evil`, wantErr: true},
		{name: "backslash in path", url: `https://example.com/path\file`, wantErr: true},
		{name: "newline", url: "https://example.com/\npath", wantErr: true},
		{name: "NUL", url: "https://example.com/\x00path", wantErr: true},
		{name: "file scheme", url: "file:///tmp/coslash", wantErr: true},
		{name: "FTP scheme", url: "ftp://example.com/file", wantErr: true},
		{name: "JavaScript scheme", url: "javascript:alert(1)", wantErr: true},
		{name: "missing scheme", url: "//example.com/path", wantErr: true},
		{name: "not a URL", url: "not a URL", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
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
