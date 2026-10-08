package main

import "testing"

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
