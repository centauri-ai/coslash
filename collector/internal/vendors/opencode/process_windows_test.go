//go:build windows

package opencode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseWindowsTUIProcessesCapturedFacts(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []tuiProcess
	}{
		{
			name: "active session",
			output: `[{"PID":22648,"StartedAt":1789672200000,"Executable":"C:\\Users\\calvin\\AppData\\Roaming\\npm\\opencode.exe",` +
				`"CommandLine":"\"C:\\Users\\calvin\\AppData\\Roaming\\npm\\opencode.exe\""}]`,
			want: []tuiProcess{{pid: 22648, startedAt: 1789672200000}},
		},
		{
			name: "resumed session",
			output: `[{"PID":23488,"StartedAt":1789672500000,"Executable":"C:\\Users\\calvin\\AppData\\Roaming\\npm\\opencode.exe",` +
				`"CommandLine":"opencode --session ses_f4e55a984ffec10MDf9LcZ9muE"}]`,
			want: []tuiProcess{{
				pid: 23488, startedAt: 1789672500000,
				sessionID: "ses_f4e55a984ffec10MDf9LcZ9muE",
			}},
		},
		{
			name: "non interactive command",
			output: `[{"PID":99,"StartedAt":1789672500000,"Executable":"C:\\opencode.exe",` +
				`"CommandLine":"opencode run"}]`,
			want: []tuiProcess{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseWindowsTUIProcesses([]byte(test.output))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("processes = %#v; want %#v", got, test.want)
			}
		})
	}
}

func TestProcessWorkingDirectoryCurrentProcess(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := processWorkingDirectory(os.Getpid()); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("working directory = %q; want %q", got, want)
	}
}

func TestMatchLiveSessionsNormalizesCapturedWindowsDirectory(t *testing.T) {
	const sessionID = "ses_capturedWindowsSession"
	processes := []tuiProcess{{
		pid:       22648,
		startedAt: 1789672200000,
		directory: filepath.Clean(`C:\coslash-discovery\opencode`),
	}}
	candidates := []liveCandidate{{
		id:        sessionID,
		directory: filepath.Clean(`C:/coslash-discovery/opencode`),
		createdAt: 1789672200000,
	}}
	if _, ok := matchLiveSessions(processes, candidates)[sessionID]; !ok {
		t.Fatal("captured Windows process and database directory did not match")
	}
}

func TestInstallPluginReplacesManagedPluginAndProtectsUnmanagedPlugin(t *testing.T) {
	source := pluginSourceForVersion("2.0.6")
	t.Run("managed", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, pluginName)
		if err := os.WriteFile(path, []byte("// managed by coSlash; old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := installPluginSource(directory, source); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, source) {
			t.Fatal("managed plugin was not replaced")
		}
	})

	t.Run("unmanaged", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, pluginName)
		want := []byte("export const userPlugin = true\n")
		if err := os.WriteFile(path, want, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := installPluginSource(directory, source); err == nil {
			t.Fatal("installPlugin overwrote an unmanaged plugin")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("unmanaged plugin changed")
		}
	})
}
