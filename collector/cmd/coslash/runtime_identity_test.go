package main

import (
	"runtime"
	"testing"
)

func TestInstallChannelDetector(t *testing.T) {
	tests := []struct {
		name, executable, resolved, home, localAppData, goos, want string
	}{
		{name: "Homebrew Cellar", executable: "/opt/homebrew/bin/coslash", resolved: "/opt/homebrew/Cellar/coslash/1.2.3/bin/coslash", goos: "darwin", want: "brew"},
		{name: "usr local script", executable: "/usr/local/bin/coslash", resolved: "/usr/local/bin/coslash", goos: "linux", want: "script"},
		{name: "user script", executable: "/Users/maya/.local/bin/coslash", resolved: "/Users/maya/.local/bin/coslash", home: "/Users/maya", goos: "darwin", want: "script"},
		{name: "Windows script", executable: `C:\Users\Maya\AppData\Local\Programs\coSlash\coslash.exe`, resolved: `C:\Users\Maya\AppData\Local\Programs\coSlash\coslash.exe`, localAppData: `C:\Users\Maya\AppData\Local`, goos: "windows", want: "windows-script"},
		{name: "path prefix is not install directory", executable: "/usr/local/bin-other/coslash", resolved: "/usr/local/bin-other/coslash", goos: "linux", want: "unknown"},
		{name: "unknown", executable: "/opt/coslash/coslash", resolved: "/opt/coslash/coslash", goos: "linux", want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := installChannelForPaths(test.executable, test.resolved, test.home, test.localAppData, test.goos); got != test.want {
				t.Fatalf("install channel=%q, want %q", got, test.want)
			}
		})
	}
}

func TestInstallChannelBuildMetadata(t *testing.T) {
	for _, test := range []struct {
		name, goos, detected, metadata, want string
	}{
		{name: "branch script at custom macOS path", goos: "darwin", detected: "unknown", metadata: "script", want: "script"},
		{name: "ordinary copied binary", goos: "darwin", detected: "unknown", want: "unknown"},
		{name: "metadata is macOS only", goos: "linux", detected: "unknown", metadata: "script", want: "unknown"},
		{name: "Homebrew path keeps precedence", goos: "darwin", detected: "brew", metadata: "script", want: "brew"},
		{name: "Windows script keeps precedence", goos: "windows", detected: "windows-script", metadata: "script", want: "windows-script"},
		{name: "unrecognized metadata", goos: "darwin", detected: "unknown", metadata: "manual", want: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := installChannelWithBuildMetadata(test.goos, test.detected, test.metadata); got != test.want {
				t.Fatalf("install channel=%q, want %q", got, test.want)
			}
		})
	}
}

func TestInstallChannelEmbeddedMetadata(t *testing.T) {
	want := "unknown"
	if runtime.GOOS == "darwin" && installChannelMetadata == "script" {
		want = "script"
	}
	if got := installChannelWithBuildMetadata(runtime.GOOS, "unknown", installChannelMetadata); got != want {
		t.Fatalf("embedded install channel=%q, want %q", got, want)
	}
}

func TestNormalizedVersion(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	tests := []struct {
		name, input, allowDev, want string
	}{
		{name: "release prefix", input: "v1.2.3", want: "1.2.3"},
		{name: "prerelease and metadata", input: "1.2.3-rc.1+build.9", want: "1.2.3-rc.1+build.9"},
		{name: "development fallback", input: "dev", want: "0.0.0"},
		{name: "development opt in", input: "dev", allowDev: "1", want: "0.0.0-dev"},
		{name: "invalid fallback", input: "not-semver", want: "0.0.0"},
		{name: "invalid numeric prerelease", input: "1.2.3-01", want: "0.0.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version = test.input
			t.Setenv("COSLASH_ALLOW_DEV_VERSION", test.allowDev)
			if got := normalizedVersion(); got != test.want {
				t.Fatalf("normalized version=%q, want %q", got, test.want)
			}
		})
	}
}
