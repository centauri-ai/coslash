package opencode

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodeMajor(t *testing.T) {
	tests := []struct {
		version string
		want    int
	}{
		{version: "opencode v2.0.6", want: 2},
		{version: "1.18.28", want: 1},
		{version: "OpenCode 3.1.0-beta.1", want: 3},
		{version: "not a version", want: 0},
	}
	for _, test := range tests {
		if got := openCodeMajor(test.version); got != test.want {
			t.Errorf("openCodeMajor(%q) = %d; want %d", test.version, got, test.want)
		}
	}
}

func TestPluginSourceForVersion(t *testing.T) {
	v1 := pluginSourceForVersion("1.18.28")
	if bytes.Contains(v1, []byte("export default")) {
		t.Fatal("OpenCode v1 plugin contains a default definition")
	}
	if !bytes.Contains(v1, []byte("export const CoslashPlugin")) {
		t.Fatal("OpenCode v1 plugin is missing its callable export")
	}

	v2 := pluginSourceForVersion("2.0.6")
	if !bytes.HasPrefix(v2, v1) {
		t.Fatal("OpenCode v2 plugin does not preserve the shared implementation")
	}
	if !bytes.Contains(v2, []byte(`export default { id: "coslash", setup: setupV2 }`)) {
		t.Fatal("OpenCode v2 plugin is missing its default definition")
	}
}

func TestInstallPluginDoesNotDowngradeWhenVersionIsUnknown(t *testing.T) {
	tests := []struct {
		name   string
		detect func() (string, error)
	}{
		{name: "missing", detect: func() (string, error) { return "", nil }},
		{name: "probe failure", detect: func() (string, error) { return "", errors.New("probe failed") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setOpenCodeVersionDetectorForTest(t, test.detect)
			directory := t.TempDir()
			path := filepath.Join(directory, pluginName)
			want := pluginSourceForVersion("2.0.6")
			if err := os.WriteFile(path, want, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := installPlugin(directory); err == nil {
				t.Fatal("installPlugin succeeded with an unknown OpenCode version")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("unknown OpenCode version changed the installed v2 plugin")
			}
		})
	}
}

func TestManagedPluginSourceRechecksVersion(t *testing.T) {
	versions := []string{"1.18.28", "2.0.6"}
	setOpenCodeVersionDetectorForTest(t, func() (string, error) {
		version := versions[0]
		versions = versions[1:]
		return version, nil
	})
	v1, err := managedPluginSource()
	if err != nil {
		t.Fatal(err)
	}
	v2, err := managedPluginSource()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(v1, []byte("export default")) {
		t.Fatal("first source is not the v1 plugin")
	}
	if !bytes.Contains(v2, []byte("export default")) {
		t.Fatal("second source did not refresh to the v2 plugin")
	}
}

func setOpenCodeVersionDetectorForTest(t *testing.T, detector func() (string, error)) {
	t.Helper()
	originalDetector := detectOpenCodeVersion
	detectOpenCodeVersion = detector
	t.Cleanup(func() {
		detectOpenCodeVersion = originalDetector
	})
}
