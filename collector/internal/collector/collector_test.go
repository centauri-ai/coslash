package collector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestApplyActivityFallbacksKeepsSessionsExportable(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(logPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := session.FileModificationTime(logPath)

	untimed := &vendors.ParsedSession{Session: &session.Session{}, LogPath: logPath}
	noLog := &vendors.ParsedSession{Session: &session.Session{LastActivityTime: 500}}
	noTimingData := &vendors.ParsedSession{Session: &session.Session{}}
	parsed := &vendors.ParsedSession{Session: &session.Session{StartedAt: 100, LastActivityTime: 900}}

	applyActivityFallbacks([]*vendors.ParsedSession{untimed, noLog, noTimingData, parsed})

	if untimed.Session.LastActivityTime != modified || untimed.Session.StartedAt != modified {
		t.Fatalf("untimed session = start %d, activity %d; want %d for both",
			untimed.Session.StartedAt, untimed.Session.LastActivityTime, modified)
	}
	if noLog.Session.StartedAt != 500 {
		t.Fatalf("session without a log path = start %d; want 500", noLog.Session.StartedAt)
	}
	if noTimingData.Session.StartedAt <= 0 || noTimingData.Session.LastActivityTime != noTimingData.Session.StartedAt {
		t.Fatalf("session without timing data = start %d, activity %d; want equal positive collection time",
			noTimingData.Session.StartedAt, noTimingData.Session.LastActivityTime)
	}
	if parsed.Session.StartedAt != 100 || parsed.Session.LastActivityTime != 900 {
		t.Fatalf("parsed timestamps were overwritten: start %d, activity %d",
			parsed.Session.StartedAt, parsed.Session.LastActivityTime)
	}
}

func TestGetSessionForPreviewLoadsOnlyTheComposedFamily(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })
	root := &vendors.ParsedSession{Session: &session.Session{
		Agent: "test", ID: "root", LastActivityTime: 100, StartedAt: 100,
		Tokens: map[string]session.ModelTokens{}, SessionDetails: session.SessionDetails{Turns: 1},
	}}
	child := &vendors.ParsedSession{Session: &session.Session{
		Agent: "test", ID: "child", LastActivityTime: 200, StartedAt: 150,
		Tokens: map[string]session.ModelTokens{},
	}, ParentID: "root"}
	collected := false
	vendorSources = []vendorSource{{
		name: "test",
		collect: func(int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
			collected = true
			return nil, nil, nil
		},
		loadFamily: func(string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
			return []*vendors.ParsedSession{root, child}, vendors.EmptySessionMetadata(), nil
		},
	}}

	got, err := GetSessionForPreview("root", 100)
	if err != nil {
		t.Fatal(err)
	}
	if collected {
		t.Fatal("preview replayed the full session list")
	}
	if got.LastActivityTime != 200 || len(got.Subagents) != 1 {
		t.Fatalf("revision = %d, subagents = %d; want 200 and 1", got.LastActivityTime, len(got.Subagents))
	}
}
