package remoteviewv1

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMaxSessionsPerAgentFitsHeavyFixture(t *testing.T) {
	heavy := heavySession()
	view := View{
		SchemaVersion:    SchemaVersion,
		CollectorVersion: "dev",
		Capabilities:     []string{CapabilityRemoteView},
		LaunchableAgents: []string{AgentClaude, AgentCodex},
		RequestedSinceMs: 0,
		RequestNowMs:     1,
		HostNowMs:        1,
		CollectedAtMs:    1,
		CoverageSinceMs:  0,
		Host:             Host{OS: "linux", Arch: "amd64"},
		Sessions:         []Session{heavy},
	}
	started := time.Now()
	one, err := Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	encodeOne := time.Since(started)
	perSession := len(one)
	// Leave envelope headroom and keep well under the wire cap.
	capacity := (MaxPayloadBytes * 3 / 4) / perSession
	if MaxSessionsPerAgent > capacity {
		t.Fatalf("MaxSessionsPerAgent=%d exceeds measured capacity %d (heavy session=%d bytes)",
			MaxSessionsPerAgent, capacity, perSession)
	}
	view.Sessions = make([]Session, 0, MaxSessionsPerAgent)
	for i := 0; i < MaxSessionsPerAgent; i++ {
		session := heavy
		session.SourceSessionID = fmt.Sprintf("22222222-2222-2222-2222-%012d", i)
		session.LastActivityAtMs = int64(10_000 + i)
		session.SessionStartedAtMs = session.LastActivityAtMs
		view.Sessions = append(view.Sessions, session)
	}
	started = time.Now()
	encoded, err := Marshal(view)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("cap-sized fixture failed: %v", err)
	}
	if len(encoded) > MaxPayloadBytes {
		t.Fatalf("encoded %d bytes exceeds 8 MiB", len(encoded))
	}
	if elapsed > 30*time.Second {
		t.Fatalf("encoding %d heavy sessions took %s", MaxSessionsPerAgent, elapsed)
	}
	t.Logf("heavy session≈%dB encodeOne=%s cap=%d encoded=%dB elapsed=%s capacity=%d",
		perSession, encodeOne, MaxSessionsPerAgent, len(encoded), elapsed, capacity)
}

func heavySession() Session {
	summary := strings.Repeat("summary ", 200)
	prompt := strings.Repeat("prompt ", 400)
	goal := strings.Repeat("goal ", 200)
	cwd := "/home/user/work/project-with-a-long-path/src"
	repo := "github.com/example/organization/repository-name"
	branch := "feature/long-branch-name-for-measurement"
	model := "claude-opus-4-20250514"
	digest := make([]Digest, 40)
	for i := range digest {
		digest[i] = Digest{
			Turn:        i,
			Category:    "user",
			Description: strings.Repeat("d", 200),
		}
	}
	todos := make([]Todo, 40)
	for i := range todos {
		todos[i] = Todo{Text: strings.Repeat("t", 80), Done: i%2 == 0}
	}
	edits := make([]FileEdit, 80)
	for i := range edits {
		edits[i] = FileEdit{
			Path:      "pkg/module/file_" + strings.Repeat("x", 20) + ".go",
			Additions: i + 1,
			Deletions: i,
			Edits:     1,
		}
	}
	commits := make([]string, 20)
	for i := range commits {
		commits[i] = strings.Repeat("c", 80)
	}
	subagents := make([]Subagent, 5)
	for i := range subagents {
		subagents[i] = Subagent{
			ID:            "sub-" + string(rune('a'+i)),
			Name:          "helper",
			Status:        "returned",
			Task:          strings.Repeat("task ", 40),
			Result:        strings.Repeat("result ", 40),
			ToolUses:      3,
			CommandLabels: []string{"git status", "go test"},
			Usage:         []ModelUsage{},
		}
	}
	return Session{
		Agent:              AgentCodex,
		SourceSessionID:    "22222222-2222-2222-2222-222222222222",
		Name:               strPtr("heavy measurement session"),
		Summary:            &summary,
		WorkingDirectory:   &cwd,
		Repository:         &repo,
		Branch:             &branch,
		SessionStartedAtMs: 1_000,
		LastActivityAtMs:   2_000,
		Model:              &model,
		DeclaredGoal:       &goal,
		FirstPrompt:        &prompt,
		Counts: Counts{
			EditedFiles: len(edits),
			Turns:       40,
			ToolUses:    80,
			Errors:      2,
			Compactions: 1,
			Commands:    10,
		},
		Usage: Usage{
			Models: []ModelUsage{{
				Model: model, InputTokens: 1000, OutputTokens: 500, EstimatedCostMicroUSD: 12345,
			}},
			EstimatedCostMicroUSD: 12345,
			UnpricedModels:        []string{},
		},
		Digest:    digest,
		Todos:     todos,
		FileEdits: edits,
		Commits:   commits,
		Git:       &GitDrift{BaseBranch: "main", Ahead: 3, Behind: 1},
		Subagents: subagents,
	}
}

func strPtr(value string) *string { return &value }
