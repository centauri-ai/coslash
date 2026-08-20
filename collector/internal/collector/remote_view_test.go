package collector

import (
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func TestBuildRemoteViewOmitsEmptyWorkingDirectoryAndExcludesLocalOnlyFields(t *testing.T) {
	commands := []string{"rm -rf /"}
	synthesis := &session.SessionSynthesis{Outcome: "secret synthesis"}
	local := &session.Session{
		Agent:            remoteviewv1.AgentClaude,
		ID:               "11111111-1111-1111-1111-111111111111",
		WorkingDirectory: "   ",
		StartedAt:        100,
		LastActivityTime: 200,
		Tokens:           map[string]session.ModelTokens{},
		SessionDetails: session.SessionDetails{
			Commands:    commands,
			Synthesis:   synthesis,
			FirstPrompt: strPtr("hello sk-abcdefghijklmnopqrstuvwxyz"),
			Digest: []session.DigestEntry{{
				Turn: 1, Category: "user", Description: "did work",
			}},
		},
	}
	view, err := BuildRemoteView([]*session.Session{local}, RemoteViewOptions{
		CollectorVersion: "dev",
		RequestedSinceMs: 0,
		RequestNowMs:     1,
		HostNowMs:        2,
		CollectedAtMs:    2,
		HostOS:           "linux",
		HostArch:         "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Sessions[0].WorkingDirectory != nil {
		t.Fatalf("cwd = %#v", view.Sessions[0].WorkingDirectory)
	}
	if view.Sessions[0].FirstPrompt == nil || strings.Contains(*view.Sessions[0].FirstPrompt, "sk-") {
		t.Fatalf("firstPrompt = %#v", view.Sessions[0].FirstPrompt)
	}
	data, err := remoteviewv1.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	if strings.Contains(encoded, "secret synthesis") || strings.Contains(encoded, "rm -rf") {
		t.Fatalf("local-only fields leaked: %s", encoded)
	}
	if !strings.Contains(encoded, `"commands":1`) {
		t.Fatalf("command count missing: %s", encoded)
	}
}

func strPtr(value string) *string { return &value }
