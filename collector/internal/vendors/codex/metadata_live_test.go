package codex

import "testing"

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
