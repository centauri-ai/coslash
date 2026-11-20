package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

type cancelAfterFirstReadSource struct {
	vendors.ReadSource
	cancel context.CancelFunc
}

func (source cancelAfterFirstReadSource) Open(path string) (io.ReadCloser, error) {
	file, err := source.ReadSource.Open(path)
	if err != nil {
		return nil, err
	}
	return &cancelAfterFirstReadCloser{ReadCloser: file, cancel: source.cancel}, nil
}

type cancelAfterFirstReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
	done   bool
}

func (reader *cancelAfterFirstReadCloser) Read(buffer []byte) (int, error) {
	n, err := reader.ReadCloser.Read(buffer)
	if n > 0 && !reader.done {
		reader.done = true
		reader.cancel()
	}
	return n, err
}

func TestRemoteParsersStopWhenContextIsCanceledDuringRead(t *testing.T) {
	tests := []struct {
		name  string
		parse func(context.Context, vendors.ReadSource, string) error
	}{
		{
			name: "Claude",
			parse: func(ctx context.Context, source vendors.ReadSource, path string) error {
				_, _, err := claude.ParseRemoteFiles(ctx, source, []string{path})
				return err
			},
		},
		{
			name: "Codex",
			parse: func(ctx context.Context, source vendors.ReadSource, path string) error {
				_, _, err := codex.ParseRemoteFilesContext(ctx, source, fakeHome, []string{path}, nil)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := cancelAfterFirstReadSource{ReadSource: vendors.LocalReadSource, cancel: cancel}
			if err := test.parse(ctx, source, path); !errors.Is(err, context.Canceled) {
				t.Fatalf("parse error = %v, want %v", err, context.Canceled)
			}
		})
	}
}
