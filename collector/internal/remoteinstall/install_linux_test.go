//go:build linux

package remoteinstall

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallKeepsOpenedVersionDirectoryAcrossIntermediateSwap(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	versionDir := filepath.Join(home, ".coslash", "helpers", "v1")
	// All components exist before the operation begins, just as they do in the
	// replacement attack.
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for directory := versionDir; directory != home; directory = filepath.Dir(directory) {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sourcePath := filepath.Join(home, "source")
	content := []byte("verified static helper bytes")
	if err := os.WriteFile(sourcePath, content, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	helpers := filepath.Dir(versionDir)
	moved := filepath.Join(home, "helpers-validated")
	attacker := filepath.Join(home, "attacker")
	if err := os.Mkdir(attacker, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	err = install(source, home, "v1", digest, func() error {
		if err := os.Rename(helpers, moved); err != nil {
			return err
		}
		return os.Symlink(attacker, helpers)
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(moved, "v1", fileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, content) {
		t.Fatalf("installed bytes = %q, want %q", installed, content)
	}
	if _, err := os.Stat(filepath.Join(attacker, "v1", fileName)); !os.IsNotExist(err) {
		t.Fatalf("attacker tree received helper: %v", err)
	}
}

func TestInstallRejectsGroupOrWorldWritableHome(t *testing.T) {
	content := []byte("verified static helper bytes")
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	sourcePath := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(sourcePath, content, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o775, 0o757} {
		home := t.TempDir()
		if err := os.Chmod(home, mode); err != nil {
			t.Fatal(err)
		}
		source, err := os.Open(sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		installErr := install(source, home, "v1", digest, nil)
		source.Close()
		if installErr == nil || !strings.Contains(installErr.Error(), "verify helper home: unsafe helper directory") {
			t.Fatalf("mode %#o: install error = %v, want unsafe helper directory", mode, installErr)
		}
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := install(source, home, "v1", digest, nil); err != nil {
		t.Fatalf("mode 0755: install error = %v", err)
	}
}
