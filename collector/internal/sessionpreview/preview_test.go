package sessionpreview

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
)

func TestBuildRejectsUnknownCost(t *testing.T) {
	repository := "github.com/centauri-ai/coslash"
	preview := Build(session.Session{
		Agent: "codex", ID: "source", Repository: &repository, StartedAt: 1, LastActivityTime: 2,
		Tokens: map[string]session.ModelTokens{},
	}, sessionexport.BuildOptions{CollectorVersion: "0.1.0"}, 2)
	if preview.State != StateInvalid || preview.ApprovalAllowed {
		t.Fatalf("preview = %#v, want non-approvable invalid state", preview)
	}
}
