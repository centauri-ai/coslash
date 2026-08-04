package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func TestCollectClassifiesSourcesAndCapsSkippedPaths(t *testing.T) {
	home := t.TempDir()
	skipped := make([]vendors.SkippedPath, 12)
	for index := range skipped {
		skipped[index] = vendors.SkippedPath{Path: filepath.Join(home, fmt.Sprintf("bad-%d", index)), Error: "denied"}
	}
	deps := Deps{
		Sources: func() []collector.SourceHealth {
			return []collector.SourceHealth{
				{Agent: "claude", Root: filepath.Join(home, ".claude", "projects"), Scan: &vendors.SourceScan{Files: []string{"one.jsonl"}, Skipped: skipped}},
				{Agent: "codex", Root: filepath.Join(home, ".codex", "sessions"), Scan: &vendors.SourceScan{RootMissing: true}},
			}
		},
		SessionCounts: func() (map[string]int, error) { return map[string]int{"claude": 1}, nil },
		LookPath: func(name string) (string, error) {
			if name == "claude" {
				return filepath.Join(home, "bin", "claude"), nil
			}
			return "", errors.New("missing")
		},
		CLIVersion:       func(context.Context, string) string { return "1.2.3" },
		Home:             func() string { return filepath.Join(home, ".coslash") },
		UserHome:         func() (string, error) { return home, nil },
		GOOS:             "darwin",
		GOARCH:           "arm64",
		Version:          "test",
		Now:              func() time.Time { return time.UnixMilli(1234) },
		SynthesisEnabled: true,
		SynthesisModel:   "model",
	}
	snapshot := Collect(context.Background(), deps)
	if snapshot.GeneratedAt != 1234 || snapshot.Sources[0].State != SourceOK || snapshot.Sources[1].State != SourceMissing {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if len(snapshot.Sources[0].Skipped) != 10 || snapshot.Sources[0].SkippedTotal != 12 {
		t.Fatalf("skipped paths were not capped: %#v", snapshot.Sources[0])
	}
	if snapshot.Sources[0].Root != "~/.claude/projects" || snapshot.Sources[0].CLI.Path != "~/bin/claude" {
		t.Fatalf("paths were not collapsed: %#v", snapshot.Sources[0])
	}
	if !snapshot.Storage.Writable || snapshot.Synthesis.Model != "model" {
		t.Fatalf("unexpected local state: %#v", snapshot)
	}
}

func TestCollectTurnsSessionCountErrorIntoFailCheck(t *testing.T) {
	home := t.TempDir()
	deps := Deps{
		Sources: func() []collector.SourceHealth {
			return []collector.SourceHealth{{Agent: "claude", Root: filepath.Join(home, ".claude"), Scan: &vendors.SourceScan{Files: []string{"one.jsonl"}}}}
		},
		SessionCounts:    func() (map[string]int, error) { return nil, errors.New("parse failed") },
		LookPath:         func(string) (string, error) { return "", errors.New("missing") },
		CLIVersion:       func(context.Context, string) string { return "" },
		Home:             func() string { return filepath.Join(home, ".coslash") },
		UserHome:         func() (string, error) { return home, nil },
		GOOS:             "darwin",
		GOARCH:           "arm64",
		Version:          "test",
		Now:              time.Now,
		SynthesisEnabled: false,
	}
	snapshot := Collect(context.Background(), deps)
	if snapshot.Checks[0].Status != StatusFail {
		t.Fatalf("source check = %#v", snapshot.Checks[0])
	}
}

func TestCollectAllowsUnknownCLIVersion(t *testing.T) {
	home := t.TempDir()
	deps := Deps{
		Sources: func() []collector.SourceHealth {
			return []collector.SourceHealth{{
				Agent: "claude",
				Root:  filepath.Join(home, ".claude"),
				Scan:  &vendors.SourceScan{Files: []string{"one.jsonl"}},
			}}
		},
		SessionCounts:    func() (map[string]int, error) { return map[string]int{"claude": 1}, nil },
		LookPath:         func(string) (string, error) { return "/opt/bin/claude", nil },
		CLIVersion:       func(context.Context, string) string { return "" },
		Home:             func() string { return filepath.Join(home, ".coslash") },
		UserHome:         func() (string, error) { return home, nil },
		GOOS:             "darwin",
		GOARCH:           "arm64",
		Version:          "test",
		Now:              time.Now,
		SynthesisEnabled: true,
	}

	snapshot := Collect(context.Background(), deps)
	if !snapshot.Sources[0].CLI.Found || snapshot.Sources[0].CLI.Version != "" {
		t.Fatalf("unexpected CLI: %#v", snapshot.Sources[0].CLI)
	}
}

func TestCollectClassifiesAnAllSkippedScanAsUnreadable(t *testing.T) {
	home := t.TempDir()
	blocked := filepath.Join(home, ".claude", "projects", "blocked")
	deps := Deps{
		Sources: func() []collector.SourceHealth {
			return []collector.SourceHealth{{
				Agent: "claude",
				Root:  filepath.Join(home, ".claude", "projects"),
				Scan: &vendors.SourceScan{
					Files:        []string{},
					Skipped:      []vendors.SkippedPath{{Path: blocked, Error: "permission denied"}},
					SkippedTotal: 1,
				},
			}}
		},
		SessionCounts:    func() (map[string]int, error) { return map[string]int{}, nil },
		LookPath:         func(string) (string, error) { return "", errors.New("missing") },
		CLIVersion:       func(context.Context, string) string { return "" },
		Home:             func() string { return filepath.Join(home, ".coslash") },
		UserHome:         func() (string, error) { return home, nil },
		GOOS:             "darwin",
		GOARCH:           "arm64",
		Version:          "test",
		Now:              time.Now,
		SynthesisEnabled: false,
	}

	snapshot := Collect(context.Background(), deps)
	if snapshot.Sources[0].State != SourceUnreadable {
		t.Fatalf("source = %#v", snapshot.Sources[0])
	}
	if snapshot.Checks[0].Status != StatusFail || !strings.Contains(snapshot.Checks[0].Detail, "~/") {
		t.Fatalf("check = %#v", snapshot.Checks[0])
	}
}
