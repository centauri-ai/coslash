package sessionbackupproducer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
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
	rootID := "11111111-2222-3333-4444-555555555555"
	dir := filepath.Join(claude.ProjectsRoot(home), "project")
	rootFile := filepath.Join(dir, rootID+".jsonl")
	childFile := filepath.Join(dir, rootID, "subagents", "agent-one.jsonl")
	if err := os.MkdirAll(filepath.Dir(childFile), 0o700); err != nil {
		t.Fatal(err)
	}
	root := []byte(`{"sessionId":"` + rootID + `","type":"user","timestamp":"2026-09-20T12:00:00Z","cwd":"` + workspace + `","message":{"content":"hello"}}` + "\n")
	child := []byte(`{"sessionId":"agent-one","type":"user","timestamp":"2026-09-20T12:00:01Z","cwd":"` + workspace + `","message":{"content":"child task"}}` + "\n")
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

func TestClaudeSSHRemainsUnsupported(t *testing.T) {
	manager := New(Options{Root: t.TempDir()})
	prepared, err := manager.Prepare(t.Context(), Selection{SourceKind: sessionbackupv1.SourceSSH, SourceID: "remote", Agent: vendors.AgentClaude, SessionID: "id"})
	var failure *PreparationError
	if prepared != nil || !errors.As(err, &failure) || failure.Coverage.Problems[0].Code != sessionbackupv1.ProblemUnsupported {
		t.Fatalf("SSH Claude = %#v, %v", prepared, err)
	}
}

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
	oldID, newID := "old-session", "new-session"
	dir := filepath.Join(claude.ProjectsRoot(home), "project")
	oldRoot := filepath.Join(dir, oldID+".jsonl")
	oldChild := filepath.Join(dir, oldID, "subagents", "agent-child.jsonl")
	newRoot := filepath.Join(dir, newID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(oldChild), 0o700); err != nil {
		t.Fatal(err)
	}
	shared := `{"sessionId":"old-session","uuid":"shared","timestamp":"2026-09-16T00:00:00Z","cwd":"` + workspace + `","type":"user","message":{"content":"hello"}}` + "\n"
	for file, data := range map[string]string{
		oldRoot:  shared,
		oldChild: `{"sessionId":"agent-child","uuid":"child-row","timestamp":"2026-09-16T00:00:01Z","cwd":"` + workspace + `","type":"user","message":{"content":"work"}}` + "\n",
		newRoot:  shared + `{"sessionId":"new-session","sessionKind":"bg","uuid":"new-row","timestamp":"2026-09-18T00:00:00Z","cwd":"` + workspace + `","type":"user","message":{"content":"continued"}}` + "\n",
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
