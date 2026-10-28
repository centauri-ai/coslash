package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/remote"
	reviewpkg "github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
)

func TestSessionListsOmitSkillBodies(t *testing.T) {
	t.Setenv("COSLASH_HOME", t.TempDir())
	const marker = "review-private-captured-skill-body"
	value := &session.Session{Agent: "claude", ID: "skill-list", LastActivityTime: 42,
		SessionDetails: session.SessionDetails{Skills: []session.Skill{{Name: "review", Content: marker + strings.Repeat("x", (1<<20)-len(marker))}}}}
	oldList := listSessions
	t.Cleanup(func() { listSessions = oldList })
	listSessions = func(context.Context, int64) ([]*session.Session, error) { return []*session.Session{value}, nil }
	handler := routesWithOnboarding(synthesis.NewManager(nil, nil), reviewpkg.NewManager(nil), settings.Open(), remote.NewManager(remote.Options{}), nil, newOnboardingManager(version))
	for _, query := range []string{"", "?sourceAware=1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions"+query, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d", response.Code)
		}
		if strings.Contains(response.Body.String(), marker) {
			t.Errorf("list %q carries full skill content: responseBytes=%d", query, response.Body.Len())
		}
	}
	if len(value.Skills) != 1 {
		t.Fatal("list mutated exact-detail source")
	}
}
