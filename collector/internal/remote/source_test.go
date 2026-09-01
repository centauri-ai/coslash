package remote

import (
	"io"
	"path"
	"testing"
	"time"
)

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
