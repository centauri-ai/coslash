package vendors

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
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
