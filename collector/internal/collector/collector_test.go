package collector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestListStopsWhenContextIsCanceled(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })

	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	vendorSources = []vendorSource{{
		name: "blocked",
		collect: func(ctx context.Context, _ int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
			close(started)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		},
	}}

	result := make(chan error, 1)
	go func() {
		_, err := List(ctx, 0)
		result <- err
	}()
	<-started
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("List continued after cancellation")
	}
}

type cancelDuringFinalizationContext struct {
	context.Context
	remaining int
}

func (ctx *cancelDuringFinalizationContext) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestFinalizeSessionsStopsDuringWork(t *testing.T) {
	ctx := &cancelDuringFinalizationContext{Context: context.Background(), remaining: 5}
	parsed := make([]*vendors.ParsedSession, 100)
	for index := range parsed {
		parsed[index] = &vendors.ParsedSession{Session: &session.Session{ID: string(rune(index + 1))}}
	}

	got, err := finalizeSessionsContext(ctx, parsed, map[string]*vendors.SessionMetadata{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("results = %#v; want nil partial results", got)
	}
}

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

	applyActivityFallbacks([]*vendors.ParsedSession{untimed, noLog, noTimingData, parsed}, true)

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
	if !noTimingData.Session.ActivityFallback {
		t.Fatal("session without timing data did not record collection-time fallback")
	}
	if parsed.Session.StartedAt != 100 || parsed.Session.LastActivityTime != 900 {
		t.Fatalf("parsed timestamps were overwritten: start %d, activity %d",
			parsed.Session.StartedAt, parsed.Session.LastActivityTime)
	}
}

func TestApplyActivityFallbacksKeepsPortableTimingDeterministic(t *testing.T) {
	sourceTimed := &vendors.ParsedSession{
		Session:         &session.Session{},
		LogModifiedAtMs: 1234,
	}
	untimed := &vendors.ParsedSession{Session: &session.Session{}}

	applyActivityFallbacks([]*vendors.ParsedSession{sourceTimed, untimed}, false)

	if sourceTimed.Session.StartedAt != 1234 || sourceTimed.Session.LastActivityTime != 1234 {
		t.Fatalf("source-timed session = start %d, activity %d; want 1234 for both",
			sourceTimed.Session.StartedAt, sourceTimed.Session.LastActivityTime)
	}
	if untimed.Session.StartedAt != 0 || untimed.Session.LastActivityTime != 0 {
		t.Fatalf("untimed portable session = start %d, activity %d; want zero for Freeze rejection",
			untimed.Session.StartedAt, untimed.Session.LastActivityTime)
	}
}

func TestResolveStatusClearsWaitingForClosedSession(t *testing.T) {
	waiting := "waiting"
	root := &vendors.ParsedSession{Session: &session.Session{
		Agent: "codex", ID: "closed", Status: &waiting,
	}}
	metadata := map[string]*vendors.SessionMetadata{
		"codex": vendors.EmptySessionMetadata(),
	}

	resolveStatus([]*vendors.ParsedSession{root}, metadata, true, true)

	if root.Session.Status != nil {
		t.Fatalf("closed session status = %q; want nil", *root.Session.Status)
	}
}

func TestFinalizeSessionsDoesNotAllocateMissingMetadata(t *testing.T) {
	metadata := vendors.EmptySessionMetadata()
	parsed := []*vendors.ParsedSession{{
		Session: &session.Session{Agent: "codex", ID: "root", StartedAt: 1, LastActivityTime: 2},
		Spawns:  map[string]vendors.SpawnState{},
	}}

	finalized := finalizeSessions(parsed, map[string]*vendors.SessionMetadata{"codex": metadata})

	if len(metadata.Sessions) != 0 {
		t.Fatalf("read-only finalization allocated %d metadata records", len(metadata.Sessions))
	}
	if finalized[0].Session.DetailRevision == "" {
		t.Fatal("local finalization did not retain a detail revision")
	}
}

func TestFinalizeSessionsReturnsSettledOpenCodeChild(t *testing.T) {
	parent := &vendors.ParsedSession{
		Session: &session.Session{
			Agent: vendors.AgentOpenCode, ID: "parent", StartedAt: 1, LastActivityTime: 4,
			Tokens: map[string]session.ModelTokens{},
		},
		Spawns: map[string]vendors.SpawnState{"child": {Completed: true}},
	}
	child := &vendors.ParsedSession{
		Session: &session.Session{
			Agent: vendors.AgentOpenCode, ID: "child", StartedAt: 2, LastActivityTime: 3,
			Tokens: map[string]session.ModelTokens{},
		},
		ParentID: "parent",
		SpawnKey: "child",
		Spawns:   map[string]vendors.SpawnState{},
	}

	roots := finalizeSessions(
		[]*vendors.ParsedSession{parent, child},
		map[string]*vendors.SessionMetadata{vendors.AgentOpenCode: vendors.EmptySessionMetadata()},
	)

	if len(roots) != 1 || len(roots[0].Session.Subagents) != 1 ||
		roots[0].Session.Subagents[0].Status != session.SubagentReturned {
		t.Fatalf("settled OpenCode family = %#v; want returned child", roots)
	}
}

func TestResolveNamesPreservesReviewNameFromPrompt(t *testing.T) {
	prompt := "Review — Fix checkout race (12345678)\n\nReview the current changes."
	root := &vendors.ParsedSession{
		Session: &session.Session{
			Agent: "codex",
			ID:    "review-id",
			SessionDetails: session.SessionDetails{
				FirstPrompt: &prompt,
			},
		},
		Name: "provider-generated title",
	}
	metadata := map[string]*vendors.SessionMetadata{
		"codex": {Sessions: map[string]*vendors.SessionEnrichment{"review-id": {Name: "metadata title"}}},
	}

	resolveNames([]*vendors.ParsedSession{root}, metadata)

	if root.Session.Name == nil || *root.Session.Name != "Review — Fix checkout race (12345678)" {
		t.Fatalf("name = %v", root.Session.Name)
	}
}

func TestResolveNamesStillPrefersMetadataForOrdinarySession(t *testing.T) {
	prompt := "Implement checkout"
	root := &vendors.ParsedSession{
		Session: &session.Session{Agent: "codex", ID: "ordinary", SessionDetails: session.SessionDetails{FirstPrompt: &prompt}},
		Name:    "prompt title",
	}
	metadata := map[string]*vendors.SessionMetadata{
		"codex": {Sessions: map[string]*vendors.SessionEnrichment{"ordinary": {Name: "metadata title"}}},
	}

	resolveNames([]*vendors.ParsedSession{root}, metadata)

	if root.Session.Name == nil || *root.Session.Name != "metadata title" {
		t.Fatalf("name = %v", root.Session.Name)
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
		collect: func(context.Context, int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
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

func TestGetSessionChangesSkipsEnvironmentProbes(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })
	vendorSources = []vendorSource{{
		name: "test",
		loadFamily: func(string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
			return []*vendors.ParsedSession{{Session: &session.Session{
				Agent: "test", ID: "root", WorkingDirectory: t.TempDir(),
				StartedAt: 100, LastActivityTime: 200,
				SessionDetails: session.SessionDetails{Turns: 1},
			}}}, vendors.EmptySessionMetadata(), nil
		},
	}}

	got, err := GetSessionChanges("test", "root")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("session changes are nil")
	}
	if got.GitProbed || got.LastEditAt != nil {
		t.Fatalf("diff read probed environment: GitProbed=%t LastEditAt=%v", got.GitProbed, got.LastEditAt)
	}
}

func TestGetSessionDetailPreservesExactSubagentText(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })
	task := strings.Repeat("task", session.TruncateTextLimit)
	result := strings.Repeat("result", session.TruncateTextLimit)
	loadFamily := func(string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
		return []*vendors.ParsedSession{
			{Session: &session.Session{
				Agent: "test", ID: "root", StartedAt: 100, LastActivityTime: 200,
				SessionDetails: session.SessionDetails{Turns: 1},
			}},
			{Session: &session.Session{
				Agent: "test", ID: "child", StartedAt: 100, LastActivityTime: 200,
				SessionDetails: session.SessionDetails{FirstPrompt: &task}, Summary: &result,
			}, ParentID: "root"},
		}, vendors.EmptySessionMetadata(), nil
	}
	vendorSources = []vendorSource{{name: "test", loadFamily: loadFamily}}

	got, err := GetSessionDetail("test", "root")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Subagents) != 1 {
		t.Fatalf("session = %#v, want one subagent", got)
	}
	if got.Subagents[0].Task != task || got.Subagents[0].Result != result {
		t.Fatalf("exact subagent text was truncated: task=%d result=%d", len(got.Subagents[0].Task), len(got.Subagents[0].Result))
	}

	parsed, metadata, err := loadFamily("root")
	if err != nil {
		t.Fatal(err)
	}
	listed := finalizeSessions(parsed, map[string]*vendors.SessionMetadata{"test": metadata})[0].Session
	if listed.Subagents[0].Task != session.Truncate(task, session.TruncateTextLimit) ||
		listed.Subagents[0].Result != session.Truncate(result, session.TruncateTextLimit) {
		t.Fatal("list projection did not use bounded subagent text")
	}
	if listed.DetailRevision != got.DetailRevision {
		t.Fatalf("list revision %q != exact revision %q", listed.DetailRevision, got.DetailRevision)
	}
}

func TestFinalizeSessionsContextPreservesDetailRevisionBeforeTruncation(t *testing.T) {
	task := strings.Repeat("task", session.TruncateTextLimit)
	result := strings.Repeat("result", session.TruncateTextLimit)
	parsed := []*vendors.ParsedSession{
		{Session: &session.Session{
			Agent: "test", ID: "root", StartedAt: 100, LastActivityTime: 200,
			SessionDetails: session.SessionDetails{Turns: 1},
		}},
		{Session: &session.Session{
			Agent: "test", ID: "child", StartedAt: 100, LastActivityTime: 200,
			SessionDetails: session.SessionDetails{FirstPrompt: &task}, Summary: &result,
		}, ParentID: "root"},
	}

	roots, err := finalizeSessionsContext(context.Background(), parsed, map[string]*vendors.SessionMetadata{
		"test": vendors.EmptySessionMetadata(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := roots[0].Session
	if got.DetailRevision == "" {
		t.Fatal("list session detail revision is empty")
	}
	if got.Subagents[0].Task != session.Truncate(task, session.TruncateTextLimit) ||
		got.Subagents[0].Result != session.Truncate(result, session.TruncateTextLimit) {
		t.Fatal("list projection did not use bounded subagent text")
	}
}

func TestGetSessionForPreviewByAgentSelectsVendor(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })
	vendorSources = []vendorSource{
		{name: "claude", loadFamily: func(string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
			return []*vendors.ParsedSession{{Session: &session.Session{Agent: "claude", ID: "same", SessionDetails: session.SessionDetails{Turns: 1}}}}, vendors.EmptySessionMetadata(), nil
		}},
		{name: "codex", loadFamily: func(string) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
			return []*vendors.ParsedSession{{Session: &session.Session{Agent: "codex", ID: "same", SessionDetails: session.SessionDetails{Turns: 1}}}}, vendors.EmptySessionMetadata(), nil
		}},
	}

	got, err := GetSessionForPreviewByAgent("codex", "same", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Agent != "codex" {
		t.Fatalf("session = %#v, want codex session", got)
	}
}

func TestGetSessionFactsByAgentSelectsVendor(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })
	vendorSources = []vendorSource{
		{name: "claude", loadFacts: func(string) (*vendors.ParsedSession, error) {
			return &vendors.ParsedSession{Session: &session.Session{Agent: "claude", ID: "same"}}, nil
		}},
		{name: "codex", loadFacts: func(string) (*vendors.ParsedSession, error) {
			return &vendors.ParsedSession{Session: &session.Session{Agent: "codex", ID: "same"}}, nil
		}},
	}

	got, err := GetSessionFactsByAgent("codex", "same")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Agent != "codex" {
		t.Fatalf("session = %#v, want codex session", got)
	}
}

func TestSessionIDIndexScansEachSourceOnce(t *testing.T) {
	original := vendorSources
	t.Cleanup(func() { vendorSources = original })
	counts := map[string]int{}
	vendorSources = []vendorSource{
		{name: "claude", collect: func(context.Context, int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
			counts["claude"]++
			return []*vendors.ParsedSession{
				{Session: &session.Session{Agent: "claude", ID: "root"}},
				{Session: &session.Session{Agent: "claude", ID: "child"}, ParentID: "root"},
			}, vendors.EmptySessionMetadata(), nil
		}},
		{name: "codex", collect: func(context.Context, int64) ([]*vendors.ParsedSession, *vendors.SessionMetadata, error) {
			counts["codex"]++
			return []*vendors.ParsedSession{{Session: &session.Session{Agent: "codex", ID: "same"}}}, vendors.EmptySessionMetadata(), nil
		}},
	}

	index, err := SessionIDIndex()
	if err != nil {
		t.Fatal(err)
	}
	if counts["claude"] != 1 || counts["codex"] != 1 {
		t.Fatalf("source scans = %#v", counts)
	}
	if !index["claude"]["root"] || index["claude"]["child"] || !index["codex"]["same"] {
		t.Fatalf("session index = %#v", index)
	}
}
