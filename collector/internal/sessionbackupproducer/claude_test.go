package sessionbackupproducer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

func claudeFixture(t *testing.T) (string, string, string, Selection) {
	t.Helper()
	home := t.TempDir()
	workspace := filepath.Join(home, "project")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	workspaceJSON, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	workspaceFileJSON, err := json.Marshal(filepath.Join(workspace, "src", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	homeFileJSON, err := json.Marshal(filepath.Join(home, ".config", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	rootID := "11111111-2222-3333-4444-555555555555"
	dir := filepath.Join(claude.ProjectsRoot(home), "project")
	rootFile := filepath.Join(dir, rootID+".jsonl")
	childFile := filepath.Join(dir, rootID, "subagents", "agent-one.jsonl")
	if err := os.MkdirAll(filepath.Dir(childFile), 0o700); err != nil {
		t.Fatal(err)
	}
	root := []byte(`{"sessionId":"` + rootID + `","type":"user","timestamp":"2026-09-20T12:00:00Z","cwd":` + string(workspaceJSON) + `,"message":{"content":"hello"}}` + "\n" +
		`{"sessionId":"` + rootID + `","type":"assistant","timestamp":"2026-09-20T12:00:02Z","toolUseResult":{"filePath":` + string(workspaceFileJSON) + `,"type":"create","content":"package main\n"}}` + "\n" +
		`{"sessionId":"` + rootID + `","type":"assistant","timestamp":"2026-09-20T12:00:03Z","toolUseResult":{"filePath":` + string(homeFileJSON) + `,"type":"create","content":"setting\n"}}` + "\n")
	child := []byte(`{"sessionId":"agent-one","type":"user","timestamp":"2026-09-20T12:00:01Z","cwd":` + string(workspaceJSON) + `,"message":{"content":"child task"}}` + "\n")
	for file, data := range map[string][]byte{rootFile: root, childFile: child, filepath.Join(filepath.Dir(childFile), "agent-one.meta.json"): []byte(`{"description":"Child work","toolUseId":"spawn-one"}`)} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, rootFile, childFile, Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentClaude, SessionID: rootID}
}

func claudeManager(root, home string, afterRawCopy func()) *Manager {
	return New(Options{Root: root, AfterRawCopy: afterRawCopy, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: vendors.LocalReadSource, Home: home}, nil
	}})
}

func TestClaudeCompleteLocalFamilyRevisionAndLastGood(t *testing.T) {
	home, rootFile, childFile, selection := claudeFixture(t)
	spool := t.TempDir()
	manager := claudeManager(spool, home, nil)
	first, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	verify := func(prepared *Prepared) {
		t.Helper()
		manifest, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
		if err != nil || manifest.CompleteBackupSHA256 != prepared.BundleID || len(manifest.Members) != 2 {
			t.Fatalf("verified manifest = %#v, %v", manifest, err)
		}
		var rawCount, sidecarCount int
		for _, artifact := range manifest.Artifacts {
			switch artifact.Kind {
			case sessionbackupv1.KindRawTranscript:
				rawCount++
			case sessionbackupv1.KindRawSidecar:
				sidecarCount++
			case sessionbackupv1.KindParsedSessionRecord:
				data, err := os.ReadFile(filepath.Join(spool, prepared.BundleID, filepath.FromSlash(artifact.LogicalName)))
				if err != nil {
					t.Fatal(err)
				}
				record, err := fullsessionv1.Decode(data)
				if err != nil || record.Session.Branch != nil {
					t.Fatalf("nullable parsed branch = %#v, %v", record.Session.Branch, err)
				}
				if artifact.MemberID == selection.SessionID {
					paths := map[string]bool{}
					for _, edit := range record.Session.FileEdits {
						paths[edit.Path] = true
					}
					if !paths["src/main.go"] || !paths["~/.config/settings.json"] {
						t.Fatalf("sanitized Claude paths = %#v", paths)
					}
				}
			}
		}
		if rawCount != 2 || sidecarCount != 1 {
			t.Fatalf("raw=%d sidecars=%d", rawCount, sidecarCount)
		}
	}
	verify(first)
	original, err := os.ReadFile(rootFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootFile, append(original, []byte(`{"sessionId":"`+selection.SessionID+`","type":"assistant","timestamp":"2026-09-20T12:00:02Z","message":{"content":"done","stop_reason":"end_turn"}}`+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Prepare(t.Context(), selection)
	if err != nil || second.BundleID == first.BundleID || second.Manifest.Source.SourceRevision == first.Manifest.Source.SourceRevision {
		t.Fatalf("changed content revision = %#v, %v", second, err)
	}
	verify(second)
	mutate := claudeManager(spool, home, func() {
		file, err := os.OpenFile(childFile, os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			_, _ = file.Write([]byte(`{"type":"user","message":{"content":"racing"}}` + "\n"))
			_ = file.Close()
		}
	})
	if result, err := mutate.Prepare(t.Context(), selection); result != nil || !errors.Is(err, ErrIncomplete) {
		t.Fatalf("racing source published = %#v, %v", result, err)
	}
	if reopened, err := manager.Open(second.BundleID); err != nil || reopened.BundleID != second.BundleID {
		t.Fatalf("last good bundle = %#v, %v", reopened, err)
	}
	if err := os.WriteFile(childFile, append(bytes.TrimSuffix(original, []byte("\n")), []byte(`{"broken":`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := manager.Prepare(t.Context(), selection); result != nil || !errors.Is(err, ErrIncomplete) {
		t.Fatalf("malformed source published = %#v, %v", result, err)
	}
}

func TestClaudeSSHCompleteFamilyAndLastGood(t *testing.T) {
	home, root, child, selection := claudeFixture(t)
	selection.SourceKind, selection.SourceID = sessionbackupv1.SourceSSH, "r_0123456789abcdef"
	source := remoteClaudeSource{home: home}
	spool := t.TempDir()
	repository := "github.com/fixture/remote"
	manager := New(Options{Root: spool, OpenSource: func(context.Context, Selection) (SourceHandle, error) {
		return SourceHandle{Source: source, Home: "/fixture/remote-home", Enrichment: map[string]remote.BackupSessionEnrichment{
			selection.SessionID: {Repository: &repository},
		}}, nil
	}})
	first, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatalf("%+v", err)
	}
	if first.Selection != selection || len(first.Manifest.Members) != 2 {
		t.Fatalf("remote family: %+v", first)
	}
	record := readRootRecord(t, spool, first)
	if record.SourceID != selection.SourceID || record.SessionID != selection.SessionID {
		t.Fatalf("remote identity: %+v", record)
	}
	original, err := os.ReadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, append(original, []byte("{broken\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := manager.Prepare(t.Context(), selection); got != nil || !errors.Is(err, ErrIncomplete) {
		t.Fatalf("partial remote family: %+v %v", got, err)
	}
	if _, err := manager.Open(first.BundleID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, original, 0o600); err != nil {
		t.Fatal(err)
	}
	original, err = os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(original, []byte("hello"), []byte("changed remote prompt"), 1)
	if err := os.WriteFile(root, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if second.BundleID == first.BundleID {
		t.Fatal("remote content change reused revision")
	}
}

type remoteClaudeSource struct{ home string }

func (s remoteClaudeSource) local(name string) string {
	return filepath.Join(s.home, filepath.FromSlash(strings.TrimPrefix(name, "/fixture/remote-home/")))
}
func (s remoteClaudeSource) Open(name string) (io.ReadCloser, error) { return os.Open(s.local(name)) }
func (s remoteClaudeSource) ReadDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(s.local(name))
}
func (s remoteClaudeSource) Stat(name string) (os.FileInfo, error) { return os.Stat(s.local(name)) }
func (s remoteClaudeSource) JoinPath(parts ...string) string       { return path.Join(parts...) }
func (s remoteClaudeSource) RelativePath(root, name string) (string, error) {
	return strings.TrimPrefix(name, root+"/"), nil
}
func (s remoteClaudeSource) DirPath(name string) string  { return path.Dir(name) }
func (s remoteClaudeSource) BasePath(name string) string { return path.Base(name) }

func TestClaudeMalformedPresentSidecarBlocksCompleteBundle(t *testing.T) {
	home, _, child, selection := claudeFixture(t)
	meta := filepath.Join(filepath.Dir(child), "agent-one.meta.json")
	if err := os.WriteFile(meta, []byte(`{"description":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := claudeManager(t.TempDir(), home, nil)
	prepared, err := manager.Prepare(t.Context(), selection)
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemInvalid {
		t.Fatalf("malformed sidecar = %#v, %v", prepared, err)
	}
}

func TestClaudeBackgroundRehomeRetainsPredecessorBytesAndChildLineage(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "project")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	workspaceJSON, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	oldID, newID := "old-session", "new-session"
	dir := filepath.Join(claude.ProjectsRoot(home), "project")
	oldRoot := filepath.Join(dir, oldID+".jsonl")
	oldChild := filepath.Join(dir, oldID, "subagents", "agent-child.jsonl")
	newRoot := filepath.Join(dir, newID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(oldChild), 0o700); err != nil {
		t.Fatal(err)
	}
	shared := `{"sessionId":"old-session","uuid":"shared","timestamp":"2026-09-16T00:00:00Z","cwd":` + string(workspaceJSON) + `,"type":"user","message":{"content":"hello"}}` + "\n"
	for file, data := range map[string]string{
		oldRoot:  shared,
		oldChild: `{"sessionId":"agent-child","uuid":"child-row","timestamp":"2026-09-16T00:00:01Z","cwd":` + string(workspaceJSON) + `,"type":"user","message":{"content":"work"}}` + "\n",
		newRoot:  shared + `{"sessionId":"new-session","sessionKind":"bg","uuid":"new-row","timestamp":"2026-09-18T00:00:00Z","cwd":` + string(workspaceJSON) + `,"type":"user","message":{"content":"continued"}}` + "\n",
	} {
		if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spool := t.TempDir()
	prepared, err := claudeManager(spool, home, nil).Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceLocal, SourceID: "local", Agent: vendors.AgentClaude, SessionID: newID})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := sessionbackupv1.VerifyDirectory(filepath.Join(spool, prepared.BundleID))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Members) != 2 || manifest.Members[0].MemberID != newID || manifest.Members[1].ParentMemberID != newID {
		t.Fatalf("rehome lineage = %#v", manifest.Members)
	}
	raw := 0
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind == sessionbackupv1.KindRawTranscript {
			raw++
		}
	}
	if raw != 3 {
		t.Fatalf("rehome raw artifacts = %d", raw)
	}
}

func claudeToolResultFixture(t *testing.T, wrapperPath string) (string, string, Selection) {
	t.Helper()
	home, rootFile, _, selection := claudeFixture(t)
	wrapper := "<persisted-output>\nOutput too large (36.1KB). Full output saved to: " + wrapperPath +
		"\n\nPreview (first 2KB):\npreview line\n...\n</persisted-output>"
	content, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	rows := `{"sessionId":"` + selection.SessionID + `","type":"assistant","timestamp":"2026-09-20T12:00:04Z","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"big","name":"Bash","input":{"command":"cat big"}}]}}` + "\n" +
		`{"sessionId":"` + selection.SessionID + `","type":"user","timestamp":"2026-09-20T12:00:05Z","message":{"content":[{"type":"tool_result","tool_use_id":"big","content":` + string(content) + `}]}}` + "\n"
	file, err := os.OpenFile(rootFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(rows); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(strings.TrimSuffix(rootFile, ".jsonl"), "tool-results", "big.txt"), selection
}

func writeToolResult(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// rootCommandOutput verifies the bundle and returns the root record's only command output and the tool-result sidecar names.
func rootCommandOutput(t *testing.T, spool string, prepared *Prepared, selection Selection) (*string, []sessionbackupv1.Artifact) {
	t.Helper()
	dir := filepath.Join(spool, prepared.BundleID)
	manifest, err := sessionbackupv1.VerifyDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	var output *string
	var sidecars []sessionbackupv1.Artifact
	for _, artifact := range manifest.Artifacts {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(artifact.LogicalName)))
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case artifact.Kind == sessionbackupv1.KindRawSidecar && strings.HasPrefix(artifact.SourceKey, "tool-result-"):
			sidecars = append(sidecars, artifact)
		case artifact.Kind == sessionbackupv1.KindParsedSessionRecord && artifact.MemberID == selection.SessionID:
			record, err := fullsessionv1.Decode(data)
			if err != nil || len(record.Session.CommandOutputs) != 1 {
				t.Fatalf("root record outputs = %v, %v", record.Session.CommandOutputs, err)
			}
			output = record.Session.CommandOutputs[0]
		}
		if artifact.Kind == sessionbackupv1.KindParsedSessionRecord && bytes.Contains(data, []byte("/Users/gone")) {
			t.Fatalf("parsed record keeps the wrapper path: %s", artifact.LogicalName)
		}
	}
	return output, sidecars
}

func TestClaudeToolResultFileIsFrozenAndCountsTowardRevision(t *testing.T) {
	home, file, selection := claudeToolResultFixture(t, "/Users/gone/.claude/projects/project/11111111-2222-3333-4444-555555555555/tool-results/big.txt")
	full := strings.Repeat("a", 9000) + strings.Repeat("z", 9000)
	writeToolResult(t, file, full)
	spool := t.TempDir()
	manager := claudeManager(spool, home, nil)
	first, err := manager.Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	output, sidecars := rootCommandOutput(t, spool, first, selection)
	if output == nil || *output != session.TruncateOutput(full) || len(sidecars) != 1 || sidecars[0].MediaType != "text/plain" || sidecars[0].ByteLength != int64(len(full)) {
		t.Fatalf("output = %v, sidecars = %+v", output, sidecars)
	}
	writeToolResult(t, file, strings.Repeat("b", 9000)+strings.Repeat("z", 9000))
	second, err := manager.Prepare(t.Context(), selection)
	if err != nil || second.Manifest.Source.SourceRevision == first.Manifest.Source.SourceRevision {
		t.Fatalf("changed tool result kept the revision: %v", err)
	}
}

func TestClaudeToolResultBlocksWhenItChanges(t *testing.T) {
	home, file, selection := claudeToolResultFixture(t, "/Users/gone/.claude/projects/project/11111111-2222-3333-4444-555555555555/tool-results/big.txt")
	writeToolResult(t, file, "first")
	racing := claudeManager(t.TempDir(), home, func() { writeToolResult(t, file, "second, longer") })
	if result, err := racing.Prepare(t.Context(), selection); result != nil || !errors.Is(err, ErrIncomplete) {
		t.Fatalf("changing tool result published = %#v, %v", result, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeUnusableToolResultFallsBackToPreview(t *testing.T) {
	home, file, selection := claudeToolResultFixture(t, "/Users/gone/.claude/projects/project/11111111-2222-3333-4444-555555555555/tool-results/big.txt")
	secret := filepath.Join(home, "secret.txt")
	writeToolResult(t, secret, "SECRET")
	for name, create := range map[string]func() error{
		"directory": func() error { return os.Mkdir(file, 0o700) },
		"symlink":   func() error { return os.Symlink(secret, file) },
		"linked-directory": func() error {
			elsewhere := filepath.Join(home, "elsewhere")
			writeToolResult(t, filepath.Join(elsewhere, "big.txt"), "OTHER")
			_ = os.RemoveAll(filepath.Dir(file))
			return os.Symlink(elsewhere, filepath.Dir(file))
		},
	} {
		_ = os.RemoveAll(filepath.Dir(file))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := create(); err != nil {
			t.Skip(name, "is not available:", err)
		}
		spool := t.TempDir()
		prepared, err := claudeManager(spool, home, nil).Prepare(t.Context(), selection)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		output, sidecars := rootCommandOutput(t, spool, prepared, selection)
		if output == nil || *output != "preview line\n..." || len(sidecars) != 0 {
			t.Fatalf("%s: output = %v, sidecars = %+v", name, output, sidecars)
		}
	}
}

func TestClaudeAbsentToolResultStaysAbsent(t *testing.T) {
	home, file, selection := claudeToolResultFixture(t, "/Users/gone/.claude/projects/project/11111111-2222-3333-4444-555555555555/tool-results/big.txt")
	spool := t.TempDir()
	prepared, err := claudeManager(spool, home, nil).Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	output, sidecars := rootCommandOutput(t, spool, prepared, selection)
	if output == nil || *output != "preview line\n..." || len(sidecars) != 0 {
		t.Fatalf("output = %v, sidecars = %+v", output, sidecars)
	}
	appearing := claudeManager(t.TempDir(), home, func() { writeToolResult(t, file, "late") })
	if result, err := appearing.Prepare(t.Context(), selection); result != nil || !errors.Is(err, ErrIncomplete) {
		t.Fatalf("appearing tool result published = %#v, %v", result, err)
	}
}

func TestClaudeToolResultOutsideOwnDirectoryIsNotCaptured(t *testing.T) {
	home, _, selection := claudeToolResultFixture(t, "/Users/gone/.claude/projects/project/99999999-9999-9999-9999-999999999999/tool-results/big.txt")
	other := filepath.Join(claude.ProjectsRoot(home), "project", "99999999-9999-9999-9999-999999999999", "tool-results", "big.txt")
	writeToolResult(t, other, "OTHER SESSION")
	spool := t.TempDir()
	prepared, err := claudeManager(spool, home, nil).Prepare(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	output, sidecars := rootCommandOutput(t, spool, prepared, selection)
	if output == nil || *output != "preview line\n..." || len(sidecars) != 0 {
		t.Fatalf("output = %v, sidecars = %+v", output, sidecars)
	}
}
