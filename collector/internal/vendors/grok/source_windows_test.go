//go:build windows

package grok

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestWindowsStoreCollectsFinishedAndOpenSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	group := filepath.Join(home, "sessions", "C%3A%5Cwork%5Crepo")
	finishedID := "01a0f48a-42bb-7802-b584-f5def46d1e75"
	openID := "01a0f8ba-e252-7c93-bced-99c163326426"
	for _, item := range []struct{ fixture, id string }{{"finished", finishedID}, {"open", openID}} {
		dir := filepath.Join(group, item.id)
		copyFixture(t, filepath.Join("testdata", item.fixture), dir)
		summary := filepath.Join(dir, "summary.json")
		body, err := os.ReadFile(summary)
		if err != nil {
			t.Fatal(err)
		}
		body = []byte(strings.ReplaceAll(string(body), "/work/repo", `C:\\work\\repo`))
		if err := os.WriteFile(summary, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	active := `[{"session_id":"` + openID + `","pid":` + strconv.Itoa(os.Getpid()) + `}]`
	if err := os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte(active), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, metadata, err := CollectContext(context.Background(), 0)
	if err != nil || len(parsed) != 2 {
		t.Fatalf("sessions = %v, err = %v", parsed, err)
	}
	byID := map[string]*vendors.ParsedSession{}
	for _, item := range parsed {
		byID[item.Session.ID] = item
		if item.Session.WorkingDirectory != `C:\work\repo` || item.Session.Agent != vendors.AgentGrok {
			t.Fatalf("Windows session = %+v", item.Session)
		}
	}
	finished, open := byID[finishedID], byID[openID]
	if finished == nil || finished.Session.Name == nil || *finished.Session.Name != "Remove Grok alias" ||
		finished.Session.Branch == nil || *finished.Session.Branch != "main" || finished.Session.FirstPrompt == nil ||
		*finished.Session.FirstPrompt != "remove the agent alias" || finished.Session.Turns != 1 ||
		finished.Session.Tokens["grok-4.7"].InputTokens != 100 || finished.RecordedCost == nil || *finished.RecordedCost != 0.754106 {
		t.Fatalf("finished session = %+v", finished)
	}
	if open == nil || !open.InTurn || open.Session.Tokens != nil || open.Session.ContextTokens != nil || open.Session.ToolUses != 1 {
		t.Fatalf("open session = %+v", open)
	}
	if !metadata.LivenessChecked || metadata.Lookup(openID).Live != "interactive" {
		t.Fatalf("active metadata = %+v", metadata)
	}
	if health := Health(); health.Missing || health.Err != nil || health.Sessions != 2 {
		t.Fatalf("health = %+v", health)
	}
}
