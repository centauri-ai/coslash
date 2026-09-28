package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
)

func TestOpenBackupSessionAuthorizesAgentBeforeSSH(t *testing.T) {
	const sourceID = "r_0123456789abcdef"
	var opened int
	manager := NewManager(Options{Open: func(_ context.Context, alias string, options OpenOptions) (*Session, error) {
		opened++
		if alias != "agent-box" || options.agent != vendors.AgentCodex {
			t.Fatalf("opened alias=%q agent=%q", alias, options.agent)
		}
		return nil, nil
	}})
	manager.cfg = &settings.RemoteSettings{ID: sourceID, SSHAlias: "agent-box", Enabled: true}
	for _, agent := range []string{"", vendors.AgentCursor, vendors.AgentOpenCode} {
		if _, err := manager.OpenBackupSession(t.Context(), sourceID, agent); !errors.Is(err, ErrRemoteSessionUnavailable) {
			t.Fatalf("%q backup session: %v", agent, err)
		}
	}
	if opened != 0 {
		t.Fatalf("unsupported source opened SSH %d times", opened)
	}
	if _, err := manager.OpenBackupSession(t.Context(), sourceID, vendors.AgentCodex); err != nil || opened != 1 {
		t.Fatalf("Codex backup session: opens=%d err=%v", opened, err)
	}
}

func TestBackupSourceOnlyReadsSelectedAgent(t *testing.T) {
	fs := newFakeFS()
	claudeFile := path.Join(fakeHome, ".claude/projects/project/session.jsonl")
	codexFile := path.Join(fakeHome, ".codex/sessions/session.jsonl")
	fs.writeFile(claudeFile, "claude", time.Unix(1, 0))
	fs.writeFile(codexFile, "codex", time.Unix(1, 0))
	source := newFakeSource(fs, Limits{})
	for _, test := range []struct{ agent, allowed, denied string }{
		{vendors.AgentClaude, claudeFile, codexFile},
		{vendors.AgentCodex, codexFile, claudeFile},
		{vendors.AgentOpenCode, "", codexFile},
		{vendors.AgentCursor, "", claudeFile},
		{"", "", codexFile},
	} {
		t.Run(test.agent, func(t *testing.T) {
			view := source.ForAgent(test.agent, 1024)
			if test.allowed != "" {
				reader, err := view.Open(test.allowed)
				if err != nil {
					t.Fatal(err)
				}
				reader.Close()
			}
			if _, err := view.Open(test.denied); !errors.Is(err, ErrPathDenied) {
				t.Fatalf("cross-agent Open: %v", err)
			}
			if _, err := view.Stat(test.denied); !errors.Is(err, ErrPathDenied) {
				t.Fatalf("cross-agent Stat: %v", err)
			}
			if _, err := view.ReadDir(path.Dir(test.denied)); !errors.Is(err, ErrPathDenied) {
				t.Fatalf("cross-agent ReadDir: %v", err)
			}
		})
	}
}

func TestBackupSourceDoesNotInspectOtherAgentRoot(t *testing.T) {
	fs := newFakeFS()
	fs.symlinkFile(path.Join(fakeHome, ".claude/projects"))
	file := path.Join(fakeHome, ".codex/sessions/session.jsonl")
	fs.writeFile(file, "codex", time.Unix(1, 0))
	if _, err := newSource(fs.ops(), Limits{}); !errors.Is(err, ErrSymlink) {
		t.Fatalf("unscoped source error = %v", err)
	}
	source, err := newSourceForAgent(fs.ops(), Limits{}, vendors.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := source.ForAgent(vendors.AgentCodex, 1024).Open(file)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if _, err := source.Stat(path.Join(fakeHome, ".claude/projects")); !errors.Is(err, ErrPathDenied) {
		t.Fatalf("scoped source could inspect Claude root: %v", err)
	}
}

func TestRemoteSourcePathOperationsUsePOSIXSemantics(t *testing.T) {
	source := newFakeSource(newFakeFS(), Limits{}).ForVendor(1024)
	joined := vendors.SourcePathJoin(source, fakeHome, ".codex", "sessions", "rollout.jsonl")
	if joined != "/home/testuser/.codex/sessions/rollout.jsonl" {
		t.Fatalf("remote joined path = %q", joined)
	}
	relative, err := vendors.SourcePathRelative(source, "/home/testuser/.codex/sessions", joined)
	if err != nil {
		t.Fatal(err)
	}
	if relative != "rollout.jsonl" {
		t.Fatalf("remote relative path = %q", relative)
	}
	if _, err := vendors.SourcePathRelative(source, "/home/testuser/.codex/sessions", "/etc/passwd"); err == nil {
		t.Fatal("relative path accepted a name outside the remote root")
	}
}

func TestClaudeWorkflowSidecarsUseRemotePOSIXPaths(t *testing.T) {
	fs := newFakeFS()
	logPath := path.Join(fakeHome, ".claude/projects/project/root/subagents/workflows/run-1/agent-child.jsonl")
	statePath := path.Join(fakeHome, ".claude/projects/project/root/workflows/run-1.json")
	journalPath := path.Join(path.Dir(logPath), "journal.jsonl")
	fs.writeFile(statePath, `{"durationMs":1,"workflowProgress":[]}`, time.Unix(100, 0))
	fs.writeFile(journalPath, `{"type":"result","agentId":"child","result":"finished"}`+"\n", time.Unix(100, 0))
	source := newFakeSource(fs, Limits{}).ForVendor(1024)
	agents := claude.WorkflowAgentsSource(source, []*vendors.ParsedSession{{
		Session: &session.Session{ID: "agent-child"},
		LogPath: logPath,
	}})
	agent := agents["agent-child"]
	if agent == nil || agent.Status() != session.SubagentReturned || agent.ResultPreview != "finished" {
		t.Fatalf("workflow agent = %#v", agent)
	}
}

func TestReadDirCacheAvoidsRedundantValidation(t *testing.T) {
	fs := newFakeFS()
	dir := path.Join(fakeHome, ".claude/projects/proj")
	fs.writeFile(path.Join(dir, "a.jsonl"), "a", time.Unix(100, 0))
	fs.writeFile(path.Join(dir, "b.jsonl"), "bb", time.Unix(200, 0))
	source := newFakeSource(fs, Limits{})

	entries, err := source.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("ReadDir: entries=%d err=%v", len(entries), err)
	}
	after := fs.counts.snapshot()

	// Stat can reuse manifest metadata. Open follows the live path and must
	// revalidate once to prevent a post-listing symlink replacement.
	if _, err := source.Stat(path.Join(dir, "a.jsonl")); err != nil {
		t.Fatalf("Stat a.jsonl: %v", err)
	}
	reader, err := source.Open(path.Join(dir, "b.jsonl"))
	if err != nil {
		t.Fatalf("Open b.jsonl: %v", err)
	}
	data, _ := io.ReadAll(reader)
	reader.Close()
	if string(data) != "bb" {
		t.Fatalf("Open b.jsonl content = %q", data)
	}

	final := fs.counts.snapshot()
	if final.LStat != after.LStat+1 || final.RealPath != after.RealPath+1 {
		t.Fatalf("cached Stat plus secure Open should add one validation: before=%+v after=%+v", after, final)
	}
	if final.Open != after.Open+1 {
		t.Fatalf("Open count = %d, want exactly one real open", final.Open-after.Open)
	}
}

func TestReadDirCacheRejectsFileReplacedBySymlink(t *testing.T) {
	fs := newFakeFS()
	dir := path.Join(fakeHome, ".claude/projects/proj")
	file := path.Join(dir, "a.jsonl")
	fs.writeFile(file, "a", time.Unix(100, 0))
	source := newFakeSource(fs, Limits{})
	if _, err := source.ReadDir(dir); err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	fs.symlinkFile(file)
	if _, err := source.Open(file); err == nil {
		t.Fatal("Open accepted a file replaced by a symlink after ReadDir")
	}
}

func TestReadDirCacheStillRejectsSymlinkEntries(t *testing.T) {
	fs := newFakeFS()
	dir := path.Join(fakeHome, ".claude/projects/proj")
	fs.symlinkFile(path.Join(dir, "evil.jsonl"))
	source := newFakeSource(fs, Limits{})

	if _, err := source.ReadDir(dir); err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if _, err := source.Stat(path.Join(dir, "evil.jsonl")); err == nil {
		t.Fatal("a symlink entry discovered via ReadDir must still be rejected")
	}
}

func TestReadDirRejectsMalformedServerEntryNames(t *testing.T) {
	fs := newFakeFS()
	dir := path.Join(fakeHome, ".claude/projects/proj")
	fs.mkdirAll(dir)
	ops := fs.ops()
	ops.readDir = func(string) ([]os.FileInfo, error) {
		return []os.FileInfo{fakeFileInfo{name: "../outside", mode: 0o644}}, nil
	}
	source, err := newSource(ops, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.ReadDir(dir); err == nil {
		t.Fatal("ReadDir accepted an entry name with traversal")
	}
}

func TestOversizedFileNeverReachesRealOpen(t *testing.T) {
	fs := newFakeFS()
	big := path.Join(fakeHome, ".codex/sessions/big.jsonl")
	fs.writeFile(big, "0123456789", time.Unix(1, 0)) // 10 bytes
	source := newFakeSource(fs, Limits{MaxFileBytes: 5})

	before := fs.counts.snapshot()
	if _, err := source.Open(big); err == nil {
		t.Fatal("expected an oversized-file error")
	}
	after := fs.counts.snapshot()
	if after.Open != before.Open {
		t.Fatalf("Open issued a real read for a file already known to be oversized: before=%d after=%d", before.Open, after.Open)
	}
}

func TestVendorBudgetsAreIndependent(t *testing.T) {
	fs := newFakeFS()
	claudeFile := path.Join(fakeHome, ".claude/projects/proj/a.jsonl")
	codexFile := path.Join(fakeHome, ".codex/sessions/rollout.jsonl")
	fs.writeFile(claudeFile, "01234", time.Unix(1, 0))
	fs.writeFile(codexFile, "01234", time.Unix(1, 0))
	source := newFakeSource(fs, Limits{MaxTotalBytes: 5, MaxFileBytes: 100})

	// A shared budget would let the Codex read exhaust the only allowance
	// before Claude's independent read gets a chance.
	claudeSource := source.ForVendor(5)
	codexSource := source.ForVendor(5)

	drain := func(rs *VendorSource, name string) error {
		reader, err := rs.Open(map[string]string{"claude": claudeFile, "codex": codexFile}[name])
		if err != nil {
			return err
		}
		defer reader.Close()
		_, err = io.ReadAll(reader)
		return err
	}
	if err := drain(codexSource, "codex"); err != nil {
		t.Fatalf("codex read within its own budget failed: %v", err)
	}
	if err := drain(claudeSource, "claude"); err != nil {
		t.Fatalf("claude read must not be starved by codex's independent budget: %v", err)
	}
}

func TestBaseSourceRetainsAggregateByteBudget(t *testing.T) {
	fs := newFakeFS()
	first := path.Join(fakeHome, ".claude/projects/proj/a.jsonl")
	second := path.Join(fakeHome, ".claude/projects/proj/b.jsonl")
	fs.writeFile(first, "123", time.Unix(1, 0))
	fs.writeFile(second, "456", time.Unix(1, 0))
	source := newFakeSource(fs, Limits{MaxTotalBytes: 5, MaxFileBytes: 100})

	reader, err := source.Open(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	reader, err = source.Open(second)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("separate base-source opens bypassed the aggregate byte budget")
	}
}
