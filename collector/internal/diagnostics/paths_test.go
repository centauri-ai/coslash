package diagnostics

import "testing"

func TestDisplayPath(t *testing.T) {
	tests := []struct {
		name string
		home string
		path string
		want string
	}{
		{name: "home", home: "/Users/alice", path: "/Users/alice", want: "~"},
		{name: "child", home: "/Users/alice", path: "/Users/alice/.claude/projects", want: "~/.claude/projects"},
		{name: "outside", home: "/Users/alice", path: "/opt/coslash", want: "/opt/coslash"},
		{name: "unknown home", path: "/Users/alice/.claude", want: "/Users/alice/.claude"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := displayPath(test.home, test.path); got != test.want {
				t.Fatalf("displayPath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDisplayErrorCollapsesHome(t *testing.T) {
	got := displayError("/Users/alice", "open /Users/alice/.claude/projects: permission denied")
	if got != "open ~/.claude/projects: permission denied" {
		t.Fatalf("displayError() = %q", got)
	}
}
