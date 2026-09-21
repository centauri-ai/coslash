package vendors

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"sync/atomic"
	"testing"
)

type cancelAfterChecks struct {
	context.Context
	remaining atomic.Int32
}

func (ctx *cancelAfterChecks) Err() error {
	if ctx.remaining.Add(-1) <= 0 {
		return context.Canceled
	}
	return nil
}

type bytesSource struct{ data []byte }

func (source bytesSource) Open(string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(source.data)), nil
}

func (bytesSource) ReadDir(string) ([]fs.DirEntry, error) { return nil, nil }
func (bytesSource) Stat(string) (fs.FileInfo, error)      { return nil, fs.ErrNotExist }

type unopenedSource struct{}

func (unopenedSource) Open(string) (io.ReadCloser, error) {
	panic("Open called after cancellation")
}

func (unopenedSource) ReadDir(string) ([]fs.DirEntry, error) {
	panic("ReadDir called after cancellation")
}

func (unopenedSource) Stat(string) (fs.FileInfo, error) {
	panic("Stat called after cancellation")
}

type cancelingReader struct{ cancel context.CancelFunc }

func (reader cancelingReader) Read([]byte) (int, error) {
	reader.cancel()
	return 0, io.EOF
}

func (cancelingReader) Close() error { return nil }

type cancelingSource struct {
	cancel context.CancelFunc
	info   fs.FileInfo
}

func (source cancelingSource) Open(string) (io.ReadCloser, error) {
	return cancelingReader{cancel: source.cancel}, nil
}

func (source cancelingSource) ReadDir(string) ([]fs.DirEntry, error) {
	source.cancel()
	return nil, nil
}

func (source cancelingSource) Stat(string) (fs.FileInfo, error) { return source.info, nil }

func TestParseFilesStopsSchedulingAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32

	got, err := ParseFilesContext(ctx, []string{"a", "b"}, func(context.Context, string) (*string, error) {
		calls.Add(1)
		value := "parsed"
		return &value, nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if calls.Load() != 0 || len(got) != 0 {
		t.Fatalf("calls = %d, results = %d; want 0, 0", calls.Load(), len(got))
	}
}

func TestParseJSONLStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := ParseJSONLSourceContext[map[string]any](ctx, unopenedSource{}, "unused.jsonl")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(got) != 0 {
		t.Fatalf("results = %d; want 0", len(got))
	}
}

func TestParseJSONLStopsDuringDecode(t *testing.T) {
	ctx := &cancelAfterChecks{Context: context.Background()}
	ctx.remaining.Store(5)
	data := bytes.Repeat([]byte("{\"value\":1}\n"), 100)

	got, err := ParseJSONLSourceContext[map[string]any](ctx, bytesSource{data: data}, "sessions.jsonl")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("results = %#v; want nil partial results", got)
	}
}

func TestParseJSONLReturnsCancellationWhenDecodeReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	got, err := ParseJSONLSourceContext[map[string]any](ctx, cancelingSource{cancel: cancel}, "sessions.jsonl")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("results = %#v; want nil", got)
	}
}

func TestWalkReadSourceReturnsCancellationAfterEmptyDirectoryRead(t *testing.T) {
	root := t.TempDir()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	err = walkReadSourceContext(ctx, cancelingSource{cancel: cancel, info: info}, root, func(string, fs.DirEntry, error) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestNewIndexedParserIndexesFilesOnce(t *testing.T) {
	files := []string{"first-a.jsonl", "b.jsonl", "second-a.jsonl"}
	ids := map[string]string{
		"first-a.jsonl":  "a",
		"b.jsonl":        "b",
		"second-a.jsonl": "a",
	}
	idCalls := 0
	var parsed []string
	load := NewIndexedParser(
		files,
		func(path string) string {
			idCalls++
			return ids[path]
		},
		func(path string) (*ParsedSession, error) {
			parsed = append(parsed, path)
			return &ParsedSession{ParentID: path}, nil
		},
	)

	for _, id := range []string{"a", "missing", "b", "a"} {
		if _, err := load(id); err != nil {
			t.Fatal(err)
		}
	}

	if idCalls != len(files) {
		t.Fatalf("ID extraction calls = %d, want %d", idCalls, len(files))
	}
	want := []string{"first-a.jsonl", "b.jsonl", "first-a.jsonl"}
	if !slices.Equal(parsed, want) {
		t.Fatalf("parsed files = %v, want %v", parsed, want)
	}
}
