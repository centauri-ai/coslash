package grok

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
)

func TestMissingSubagentMetaRecoversStructuredEvents(t *testing.T) {
	spawn := `{"params":{"sessionId":"parent","update":{"sessionUpdate":"subagent_spawned","subagent_id":"sub","attempt_id":"current","parent_session_id":"parent","child_session_id":"child","description":"Check paths","model":"grok-4.7"}}}`
	finish := `{"params":{"sessionId":"parent","update":{"sessionUpdate":"subagent_finished","subagent_id":"sub","attempt_id":"current","child_session_id":"child","status":"completed","duration_ms":1961,"tool_calls":3,"output":"CHILD_WINDOWS_OK"}}}`
	for _, tt := range []struct {
		name, spawn, finish, meta, childParent, sidecar string
		wantChild                                       bool
		status                                          string
		result                                          string
	}{
		{name: "completed", spawn: spawn, finish: finish, wantChild: true, status: session.SubagentReturned, result: "CHILD_WINDOWS_OK"},
		{name: "failed", spawn: spawn, finish: strings.ReplaceAll(finish, "completed", "failed"), wantChild: true, status: session.SubagentAborted, result: "CHILD_WINDOWS_OK"},
		{name: "stale finish", spawn: spawn, finish: strings.ReplaceAll(finish, "current", "previous"), wantChild: true, status: session.SubagentRunning},
		{name: "wrong parent", spawn: strings.ReplaceAll(spawn, `"parent_session_id":"parent"`, `"parent_session_id":"other"`), finish: finish},
		{name: "wrong session", spawn: strings.ReplaceAll(spawn, `"sessionId":"parent"`, `"sessionId":"other"`), finish: finish},
		{name: "finish without spawn", finish: finish},
		{name: "malformed spawn", spawn: spawn[:len(spawn)-1], finish: finish},
		{name: "declared parent wins", spawn: spawn, finish: finish, childParent: "other"},
		{name: "metadata wins", spawn: spawn, finish: finish, meta: `{"child_session_id":"child","description":"metadata","status":"failed"}`, wantChild: true, status: session.SubagentAborted},
		{name: "truncated metadata", spawn: spawn, finish: finish, meta: `{"child_session_id":"child"`, wantChild: true, status: session.SubagentReturned, result: "CHILD_WINDOWS_OK"},
		{name: "sidecar without event output", spawn: spawn, finish: strings.ReplaceAll(finish, `,"output":"CHILD_WINDOWS_OK"`, ""), sidecar: `{"output":"CHILD_WINDOWS_OK"}`, wantChild: true, status: session.SubagentReturned, result: "CHILD_WINDOWS_OK"},
		{name: "sidecar wins over event", spawn: spawn, finish: strings.ReplaceAll(finish, "CHILD_WINDOWS_OK", "event result"), meta: `{"child_session_id":"child"`, sidecar: `{"output":"CHILD_WINDOWS_OK"}`, wantChild: true, status: session.SubagentReturned, result: "CHILD_WINDOWS_OK"},
		{name: "invalid sidecar keeps event output", spawn: spawn, finish: finish, sidecar: `{`, wantChild: true, status: session.SubagentReturned, result: "CHILD_WINDOWS_OK"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), strings.Repeat("long custom path ", 9)+"home")
			t.Setenv("GROK_HOME", home)
			group := filepath.Join(home, "sessions", "C%3A%5Cwork%5Crepo")
			parentDir := filepath.Join(group, "parent")
			childDir := filepath.Join(group, "child")
			writeSummary(t, parentDir, `{"info":{"id":"parent","cwd":"C:\\work\\repo"},"chat_format_version":1}`)
			writeSummary(t, childDir, `{"info":{"id":"child","cwd":"C:\\work\\repo"},"chat_format_version":1,"session_kind":"subagent","parent_session_id":"`+tt.childParent+`"}`)
			metaDir := filepath.Join(parentDir, "subagents", "sub")
			if err := os.MkdirAll(metaDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.meta != "" {
				if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(tt.meta), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tt.sidecar != "" {
				if err := os.WriteFile(filepath.Join(metaDir, "output.json"), []byte(tt.sidecar), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writeUpdates(t, parentDir, []string{tt.spawn, tt.finish, tt.finish})
			// Only the child is recent: collection must still include its recovered parent.
			old, recent := time.Unix(1000, 0), time.Unix(3000, 0)
			for _, path := range []string{filepath.Join(parentDir, "summary.json"), filepath.Join(parentDir, "updates.jsonl")} {
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chtimes(filepath.Join(childDir, "summary.json"), recent, recent); err != nil {
				t.Fatal(err)
			}
			for _, since := range []int64{0, time.Unix(2000, 0).UnixMilli()} {
				parsed, _, err := CollectContext(context.Background(), since)
				if err != nil {
					t.Fatal(err)
				}
				linked := 0
				for _, item := range parsed {
					if item.Session.ID == "child" && item.ParentID == "parent" {
						linked++
						if item.Result != tt.result {
							t.Fatalf("since=%d result=%q, want %q", since, item.Result, tt.result)
						}
					}
				}
				if (linked == 1) != tt.wantChild {
					t.Fatalf("since=%d linked=%d, want child=%v", since, linked, tt.wantChild)
				}
				if tt.wantChild && len(parsed) != 2 {
					t.Fatalf("since=%d family size=%d", since, len(parsed))
				}
			}
			parent, err := GetSessionFacts("parent")
			if err != nil || parent == nil {
				t.Fatalf("parent=%v err=%v", parent, err)
			}
			if !tt.wantChild {
				if len(parent.Session.Subagents) != 0 {
					t.Fatalf("unexpected children: %+v", parent.Session.Subagents)
				}
				return
			}
			if len(parent.Session.Subagents) != 1 {
				t.Fatalf("children=%+v", parent.Session.Subagents)
			}
			child := parent.Session.Subagents[0]
			if child.ID != "child" || child.Status != tt.status || child.Result != tt.result {
				t.Fatalf("child=%+v", child)
			}
			if tt.result != "" && (child.Name != "Check paths" || child.DurationMs == nil || *child.DurationMs != 1961 || child.ToolUses != 3 || child.Model == nil || *child.Model != "grok-4.7") {
				t.Fatalf("recovered fields=%+v", child)
			}
			family, _, err := GetSessionFamily("child")
			if err != nil || len(family) != 2 || family[0].Session.ID != "parent" {
				t.Fatalf("family=%+v err=%v", family, err)
			}
		})
	}
}

func TestSubagentFallbackPreservesMetadataAndLineBound(t *testing.T) {
	dir := t.TempDir()
	writeSummary(t, dir, `{"info":{"id":"parent"},"chat_format_version":1}`)
	for _, name := range []string{"valid", "missing"} {
		if err := os.MkdirAll(filepath.Join(dir, "subagents", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "subagents", "valid", "meta.json"), []byte(`{"child_session_id":"child","description":"authoritative","status":"failed"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUpdates(t, dir, []string{`{"params":{"sessionId":"parent","update":{"sessionUpdate":"subagent_spawned","subagent_id":"missing","parent_session_id":"parent","child_session_id":"child","description":"fallback"}}}`})
	if metas := readSubagentMetas(context.Background(), dir, true); len(metas) != 1 || metas[0].Description != "authoritative" {
		t.Fatalf("metadata=%+v", metas)
	}
	file, err := os.OpenFile(filepath.Join(dir, "updates.jsonl"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(maxGrokJSONBytes + 1)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("truncate=%v close=%v", err, closeErr)
	}
	if metas := readSubagentMetas(context.Background(), dir, true); len(metas) != 1 || metas[0].Description != "authoritative" {
		t.Fatalf("oversized fallback metadata=%+v", metas)
	}
}

func TestSubagentRecoveryStreamsLargeLogsAndIndexesOnlyLineage(t *testing.T) {
	dir := t.TempDir()
	writeSummary(t, dir, `{"info":{"id":"parent"},"chat_format_version":1}`)
	for _, name := range []string{"valid", "missing"} {
		if err := os.MkdirAll(filepath.Join(dir, "subagents", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "subagents", "valid", "meta.json"), []byte(`{"child_session_id":"valid-child","prompt":"task","output":"sidecar result"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, "updates.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	line := "{}" + strings.Repeat(" ", 64*1024-3) + "\n"
	for written := int64(0); written <= maxGrokJSONBytes; written += int64(len(line)) {
		if _, err := file.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range []string{
		`{"params":{"sessionId":"parent","update":{"sessionUpdate":"subagent_spawned","subagent_id":"missing","parent_session_id":"parent","child_session_id":"child","prompt":"task","description":"name"}}}`,
		`{"params":{"sessionId":"parent","update":{"sessionUpdate":"subagent_finished","subagent_id":"missing","child_session_id":"child","status":"completed","output":"recovered result"}}}`,
	} {
		if _, err := file.WriteString(event + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, details := range []bool{false, true} {
		metas := readSubagentMetas(context.Background(), dir, details)
		if len(metas) != 2 {
			t.Fatalf("details=%v metadata=%+v", details, metas)
		}
		for _, meta := range metas {
			if !details && meta != (subagentMeta{ChildSessionID: meta.ChildSessionID}) {
				t.Fatalf("archive index retained details: %+v", meta)
			}
			if details && meta.ChildSessionID == "child" && (meta.Status != "completed" || meta.Output != "recovered result" || meta.dir != filepath.Join(dir, "subagents", "missing")) {
				t.Fatalf("large-log recovery=%+v", meta)
			}
		}
	}
}
