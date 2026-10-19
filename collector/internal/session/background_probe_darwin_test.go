package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithinPathRespectsCaseSensitivity(t *testing.T) {
	const protected = "/Users/example/Documents"
	for _, test := range []struct {
		name          string
		path          string
		caseSensitive bool
		want          bool
	}{
		{name: "exact root", path: protected, caseSensitive: true, want: true},
		{name: "child", path: protected + "/work", caseSensitive: true, want: true},
		{name: "case distinct directory", path: "/Users/example/documents/work", caseSensitive: true, want: false},
		{name: "case insensitive alias", path: "/Users/example/documents/work", caseSensitive: false, want: true},
		{name: "sibling prefix", path: protected + "-backup/work", caseSensitive: true, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := withinPath(test.path, protected, test.caseSensitive); got != test.want {
				t.Fatalf("withinPath(%q, %q, %t) = %t, want %t", test.path, protected, test.caseSensitive, got, test.want)
			}
		})
	}
}

func TestBackgroundFilesystemProbeResolvesSymlinksBeforeAllowing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	protected := filepath.Join(home, "Documents", "work")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(home, "Projects", "work")
	if err := os.MkdirAll(allowed, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(protected, alias); err != nil {
		t.Fatal(err)
	}
	if backgroundFilesystemProbeAllowed(alias) {
		t.Fatal("symlink into Documents was allowed")
	}

	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(allowed, alias); err != nil {
		t.Fatal(err)
	}
	if !resolvedBackgroundProbePathAllowed(alias, home, true, true) {
		t.Fatal("symlink to an allowed project was rejected")
	}
}

func TestReadVolumeCaseSensitivity(t *testing.T) {
	if _, ok := readVolumeCaseSensitivity("/"); !ok {
		t.Fatal("could not read the root volume's case sensitivity")
	}
}

func TestBackgroundFilesystemProbeRejectsUncheckedSymlinkTargets(t *testing.T) {
	home := t.TempDir()
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(filepath.Join(home, "missing"), alias); err != nil {
		t.Fatal(err)
	}
	if resolvedBackgroundProbePathAllowed(alias, home, true, true) {
		t.Fatal("symlink with a missing target was allowed")
	}
}
