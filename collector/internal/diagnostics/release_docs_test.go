package diagnostics

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestInstallationDocAssetNamesMatchReleasePlatforms(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	collectorRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	makefile, err := os.ReadFile(filepath.Join(collectorRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join(collectorRoot, "..", "docs", "remote-host-installation.md"))
	if err != nil {
		t.Fatal(err)
	}
	platformsLine := ""
	for _, line := range strings.Split(string(makefile), "\n") {
		if strings.HasPrefix(line, "PLATFORMS :=") {
			platformsLine = strings.TrimSpace(strings.TrimPrefix(line, "PLATFORMS :="))
			break
		}
	}
	if platformsLine == "" {
		t.Fatal("PLATFORMS missing from Makefile")
	}
	docText := string(doc)
	if !strings.Contains(docText, "Install the Linux remote collector") {
		t.Fatal("unexpected installation doc")
	}
	for _, platform := range strings.Fields(platformsLine) {
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok {
			t.Fatalf("bad platform %q", platform)
		}
		asset := "coslash_<VERSION>_" + goos + "_" + goarch + ".tar.gz"
		if !strings.Contains(docText, asset) {
			t.Fatalf("docs missing asset %s", asset)
		}
	}
	if !strings.Contains(docText, "checksums.txt") {
		t.Fatal("docs missing checksums.txt")
	}
	if !regexp.MustCompile(`~/.local/bin/coslash`).MatchString(docText) {
		t.Fatal("docs missing fixed install path")
	}
	if !strings.Contains(docText, "snapshot --probe") {
		t.Fatal("docs missing probe verification")
	}
}
