package pi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestPiReadersRequireRegularFilesAndPreserveCancellation(t *testing.T) {
	readers := []struct {
		name string
		read func(context.Context, string) error
	}{
		{"header", func(ctx context.Context, path string) error { _, err := readTranscriptHeader(ctx, path); return err }},
		{"transcript", func(ctx context.Context, path string) error { _, err := parseTranscriptContext(ctx, path); return err }},
		{"project settings", func(ctx context.Context, path string) error { _, err := readProjectSettings(ctx, path); return err }},
	}
	regular := captureTranscript(t, "regular", "")
	directory := t.TempDir()
	before, err := os.ReadFile(regular)
	if err != nil {
		t.Fatal(err)
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			if err := reader.read(t.Context(), directory); !errors.Is(err, vendors.ErrInvalidData) {
				t.Fatalf("non-regular input = %v", err)
			}
			if err := reader.read(t.Context(), regular); err != nil {
				t.Fatalf("regular input = %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			for _, path := range []string{directory, regular, filepath.Join(directory, "missing")} {
				if err := reader.read(ctx, path); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled read = %v", err)
				}
			}
		})
	}
	after, err := os.ReadFile(regular)
	if err != nil || string(before) != string(after) {
		t.Fatal("read changed the transcript", err)
	}
}
