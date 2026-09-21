package opencode

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestInstallPluginDoesNotDowngradeWhenVersionProbeFails(t *testing.T) {
	resetManagedSourceCacheForTest(t)
	detectOpenCodeVersion = func() (string, error) {
		return "", errors.New("probe failed")
	}

	directory := t.TempDir()
	path := filepath.Join(directory, pluginName)
	want := pluginSourceForVersion("2.0.6")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installPlugin(directory); err == nil {
		t.Fatal("installPlugin succeeded after an indeterminate version probe")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("version probe failure changed the installed v2 plugin")
	}
}

func TestManagedPluginSourceCachesVersionDecision(t *testing.T) {
	resetManagedSourceCacheForTest(t)
	calls := 0
	detectOpenCodeVersion = func() (string, error) {
		calls++
		return "2.0.6", nil
	}
	for range 2 {
		source, err := managedPluginSource()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(source, []byte("export default")) {
			t.Fatal("cached source is not the v2 plugin")
		}
	}
	if calls != 1 {
		t.Fatalf("version detector called %d times; want 1", calls)
	}
}

func resetManagedSourceCacheForTest(t *testing.T) {
	t.Helper()
	originalDetector := detectOpenCodeVersion
	managedSourceCache.Lock()
	originalSource := managedSourceCache.source
	managedSourceCache.source = nil
	managedSourceCache.Unlock()
	t.Cleanup(func() {
		detectOpenCodeVersion = originalDetector
		managedSourceCache.Lock()
		managedSourceCache.source = originalSource
		managedSourceCache.Unlock()
	})
}
