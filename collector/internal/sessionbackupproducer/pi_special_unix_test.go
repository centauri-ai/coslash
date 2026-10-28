//go:build darwin || linux

package sessionbackupproducer

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestPiFreezerRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// An attached writer lets a missing type guard fail without hanging CI.
	pipe, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	file, _, err := openPiInput(t.Context(), path)
	if file != nil {
		file.Close()
	}
	if !errors.Is(err, vendors.ErrInvalidData) {
		t.Fatalf("FIFO input = %v", err)
	}
}
