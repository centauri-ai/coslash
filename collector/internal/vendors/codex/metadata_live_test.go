package codex

import (
	"errors"
	"testing"
)

func TestSessionIDForOpenRollout(t *testing.T) {
	tests := []struct {
		name string
		path string
		pids []uint32
		want string
	}{
		{
			name: "active process",
			path: `C:\Users\calvin\.codex\sessions\2026\09\17\rollout-2026-09-17T10-00-00-01a0b1a9-d254-7162-af80-9e5d3a5afc3e.jsonl`,
			pids: []uint32{39044},
			want: "01a0b1a9-d254-7162-af80-9e5d3a5afc3e",
		},
		{
			name: "resumed process",
			path: `C:\Users\calvin\.codex\sessions\2026\09\17\rollout-2026-09-17T10-00-00-01a0b1a9-d254-7162-af80-9e5d3a5afc3e.jsonl`,
			pids: []uint32{40900},
			want: "01a0b1a9-d254-7162-af80-9e5d3a5afc3e",
		},
		{
			name: "closed rollout",
			path: `C:\Users\calvin\.codex\sessions\2026\09\17\rollout-2026-09-17T10-00-00-01a0b1a9-d254-7162-af80-9e5d3a5afc3e.jsonl`,
			pids: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionIDForOpenRollout(test.path, test.pids); got != test.want {
				t.Fatalf("sessionIDForOpenRollout(%q, %v) = %q, want %q", test.path, test.pids, got, test.want)
			}
		})
	}
}

func TestPreserveOperationError(t *testing.T) {
	operationErr := errors.New("operation failed")
	cleanupErr := errors.New("cleanup failed")
	tests := []struct {
		name      string
		operation error
		cleanup   error
		want      error
	}{
		{name: "operation error wins", operation: operationErr, cleanup: cleanupErr, want: operationErr},
		{name: "cleanup error is returned", cleanup: cleanupErr, want: cleanupErr},
		{name: "success", want: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := preserveOperationError(test.operation, test.cleanup); got != test.want {
				t.Fatalf("preserveOperationError(%v, %v) = %v, want %v", test.operation, test.cleanup, got, test.want)
			}
		})
	}
}
