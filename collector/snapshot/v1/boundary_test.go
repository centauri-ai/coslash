package snapshotv1

import (
	"fmt"
	"strings"
	"testing"
)

func TestBoundedWireFieldsAcceptExactUTF8BytesAndRejectOneMore(t *testing.T) {
	tests := []struct {
		name   string
		limit  int
		assign func(*Snapshot, string)
	}{
		{"collector version", MaxCollectorVersionBytes, func(s *Snapshot, v string) { s.CollectorVersion = v }},
		{"source session id", MaxIdentifierBytes, func(s *Snapshot, v string) { s.SourceSessionID = v }},
		{"repository", MaxRepositoryBytes, func(s *Snapshot, v string) { s.Repository.Canonical = v }},
		{"session name", MaxNameBytes, func(s *Snapshot, v string) { s.Session.Name = &v }},
		{"session summary", MaxSummaryBytes, func(s *Snapshot, v string) { s.Session.Summary = &v }},
		{"session status", 64, func(s *Snapshot, v string) { s.Session.Status = &v }},
		{"working directory", MaxPathBytes, func(s *Snapshot, v string) { s.Session.WorkingDirectory = &v }},
		{"branch", MaxBranchBytes, func(s *Snapshot, v string) { s.Session.Branch = &v }},
		{"entrypoint", MaxEntrypointBytes, func(s *Snapshot, v string) { s.Session.Entrypoint = &v }},
		{"session model", MaxModelBytes, func(s *Snapshot, v string) { s.Session.Model = &v }},
		{"declared goal", MaxGoalBytes, func(s *Snapshot, v string) { s.Session.DeclaredGoal = &v }},
		{"first prompt", MaxPromptBytes, func(s *Snapshot, v string) { s.Session.FirstPrompt = &v }},
		{"usage model", MaxModelBytes, func(s *Snapshot, v string) { s.Session.Usage.Models = []ModelUsage{{Model: v}} }},
		{"unpriced model", MaxModelBytes, func(s *Snapshot, v string) { s.Session.Usage.UnpricedModels = []string{v} }},
		{"digest category", 64, func(s *Snapshot, v string) { s.Session.Digest = []Digest{{Category: v}} }},
		{"digest description", MaxDigestTextBytes, func(s *Snapshot, v string) { s.Session.Digest = []Digest{{Category: "user", Description: v}} }},
		{"digest answer", MaxDigestTextBytes, func(s *Snapshot, v string) { s.Session.Digest = []Digest{{Category: "user", Answer: &v}} }},
		{"digest subagent id", MaxIdentifierBytes, func(s *Snapshot, v string) { s.Session.Digest = []Digest{{Category: "user", SubagentID: &v}} }},
		{"todo text", MaxTodoTextBytes, func(s *Snapshot, v string) { s.Session.Todos = []Todo{{Text: v}} }},
		{"file edit path", MaxPathBytes, func(s *Snapshot, v string) { s.Session.FileEdits = []FileEdit{{Path: v}} }},
		{"commit subject", MaxCommitTextBytes, func(s *Snapshot, v string) { s.Session.Commits = []string{v} }},
		{"git base branch", MaxBranchBytes, func(s *Snapshot, v string) { s.Session.Git = &GitDrift{BaseBranch: v} }},
		{"subagent id", MaxIdentifierBytes, func(s *Snapshot, v string) {
			a := validBoundarySubagent()
			a.ID = v
			s.Session.Subagents = []Subagent{a}
		}},
		{"subagent name", MaxNameBytes, func(s *Snapshot, v string) {
			a := validBoundarySubagent()
			a.Name = v
			s.Session.Subagents = []Subagent{a}
		}},
		{"subagent model", MaxModelBytes, func(s *Snapshot, v string) {
			a := validBoundarySubagent()
			a.Model = &v
			s.Session.Subagents = []Subagent{a}
		}},
		{"subagent status", 64, func(s *Snapshot, v string) {
			a := validBoundarySubagent()
			a.Status = v
			s.Session.Subagents = []Subagent{a}
		}},
		{"subagent task", MaxSubagentTextBytes, func(s *Snapshot, v string) {
			a := validBoundarySubagent()
			a.Task = v
			s.Session.Subagents = []Subagent{a}
		}},
		{"subagent result", MaxSubagentTextBytes, func(s *Snapshot, v string) {
			a := validBoundarySubagent()
			a.Result = v
			s.Session.Subagents = []Subagent{a}
		}},
		{"command label", MaxCommandLabelBytes, func(s *Snapshot, v string) {
			a := validBoundarySubagent()
			a.CommandLabels = []string{v}
			s.Session.Subagents = []Subagent{a}
		}},
		{"subagent usage model", MaxModelBytes, func(s *Snapshot, v string) {
			a := validBoundarySubagent()
			a.Usage = []ModelUsage{{Model: v}}
			s.Session.Subagents = []Subagent{a}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exact := strings.Repeat("é", test.limit/2)
			if len(exact) != test.limit {
				t.Fatalf("test setup produced %d bytes; want %d", len(exact), test.limit)
			}
			valid := validSnapshot()
			test.assign(&valid, exact)
			if _, err := Marshal(valid); err != nil {
				t.Fatalf("exact %d-byte UTF-8 value rejected: %v", test.limit, err)
			}

			over := validSnapshot()
			test.assign(&over, exact+"x")
			if _, err := Marshal(over); err == nil {
				t.Fatalf("%d-byte value accepted; limit is %d", test.limit+1, test.limit)
			}
		})
	}
}

func TestBoundedWireCollectionsAcceptExactLimitAndRejectOneMore(t *testing.T) {
	tests := []struct {
		name   string
		limit  int
		assign func(*Snapshot, int)
	}{
		{"usage models", MaxUsageModels, func(s *Snapshot, n int) { s.Session.Usage.Models = boundaryUsage(n) }},
		{"unpriced models", MaxUnpricedModels, func(s *Snapshot, n int) { s.Session.Usage.UnpricedModels = boundaryStrings("u", n) }},
		{"digest", MaxDigestItems, func(s *Snapshot, n int) {
			s.Session.Digest = make([]Digest, n)
			for i := range s.Session.Digest {
				s.Session.Digest[i].Category = "x"
			}
		}},
		{"todos", MaxTodoItems, func(s *Snapshot, n int) { s.Session.Todos = make([]Todo, n) }},
		{"file edits", MaxFileEditItems, func(s *Snapshot, n int) {
			s.Session.FileEdits = make([]FileEdit, n)
			for i := range s.Session.FileEdits {
				s.Session.FileEdits[i].Path = "x"
			}
		}},
		{"commits", MaxCommitItems, func(s *Snapshot, n int) { s.Session.Commits = make([]string, n) }},
		{"subagents", MaxSubagentItems, func(s *Snapshot, n int) {
			s.Session.Subagents = make([]Subagent, n)
			for i := range s.Session.Subagents {
				s.Session.Subagents[i] = validBoundarySubagent()
			}
		}},
		{"command labels", MaxCommandLabelItems, func(s *Snapshot, n int) {
			a := validBoundarySubagent()
			a.CommandLabels = make([]string, n)
			for i := range a.CommandLabels {
				a.CommandLabels[i] = "x"
			}
			s.Session.Subagents = []Subagent{a}
		}},
		{"subagent usage models", MaxUsageModels, func(s *Snapshot, n int) {
			a := validBoundarySubagent()
			a.Usage = boundaryUsage(n)
			s.Session.Subagents = []Subagent{a}
		}},
		{"truncation metadata", 5000, func(s *Snapshot, n int) {
			s.Truncation = make([]Truncation, n)
			for i := range s.Truncation {
				s.Truncation[i] = Truncation{Path: "/session", Reason: TruncationReasonTextBudget}
			}
		}},
		{"redaction metadata", 5000, func(s *Snapshot, n int) {
			s.Redactions = make([]Redaction, n)
			for i := range s.Redactions {
				s.Redactions[i] = Redaction{Path: "/session", Reason: "x"}
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			valid := validSnapshot()
			test.assign(&valid, test.limit)
			if _, err := Marshal(valid); err != nil {
				t.Fatalf("exact item limit %d rejected: %v", test.limit, err)
			}

			over := validSnapshot()
			test.assign(&over, test.limit+1)
			if _, err := Marshal(over); err == nil {
				t.Fatalf("%d items accepted; limit is %d", test.limit+1, test.limit)
			}
		})
	}
}

func boundaryUsage(n int) []ModelUsage {
	result := make([]ModelUsage, n)
	for i := range result {
		result[i].Model = fmt.Sprintf("m%05d", i)
	}
	return result
}

func boundaryStrings(prefix string, n int) []string {
	result := make([]string, n)
	for i := range result {
		result[i] = fmt.Sprintf("%s%05d", prefix, i)
	}
	return result
}

func validBoundarySubagent() Subagent {
	return Subagent{
		ID: "child", Name: "worker", Status: "returned",
		CommandLabels: []string{}, Usage: []ModelUsage{},
	}
}
