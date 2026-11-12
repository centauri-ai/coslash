package session

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRepositoryRootIgnoresEmptyGitDirectory(t *testing.T) {
	home := t.TempDir()
	mkdir(t, home, ".git")
	project := mkdir(t, home, "codex_work")
	if root := RepositoryRoot(project); root != "" {
		t.Fatalf("root = %q; an empty .git directory is not a repository", root)
	}
	if name, localOnly := CanonicalRepositoryName(project); name != "codex_work" || !localOnly {
		t.Fatalf("name = %q localOnly = %v", name, localOnly)
	}
	worktree := mkdir(t, home, "worktrees", "app")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if root := RepositoryRoot(filepath.Join(worktree)); root != worktree {
		t.Fatalf("worktree root = %q; want %q", root, worktree)
	}
}

func TestRemovedFolderKeepsRememberedRepository(t *testing.T) {
	t.Cleanup(func() { UseRepositoryMemory("") })
	dir := t.TempDir()
	memory := filepath.Join(dir, "state", "repositories.json")
	worktree := mkdir(t, dir, "worktrees", "fix-sidebar")
	repository := mkdir(t, dir, "local-repo")
	mkdir(t, repository, ".git")
	if err := os.WriteFile(filepath.Join(repository, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	UseRepositoryMemory(memory)
	RememberRepository(worktree, "github.com/acme/coslash")
	RememberRepository(repository, "github.com/acme/old")
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	UseRepositoryMemory(memory) // reload from disk, as a restart does
	if name, localOnly := CanonicalRepositoryName(worktree); name != "github.com/acme/coslash" || localOnly {
		t.Fatalf("removed worktree = %q localOnly = %v", name, localOnly)
	}
	if name, localOnly := CanonicalRepositoryName(repository); name != "local-repo" || !localOnly {
		t.Fatalf("a repository without a remote must trust Git: %q localOnly = %v", name, localOnly)
	}
	if info, err := os.Stat(memory); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("memory file = %v err = %v", info, err)
	}

	UseRepositoryMemory("")
	if name, localOnly := CanonicalRepositoryName(worktree); name != "fix-sidebar" || !localOnly {
		t.Fatalf("memory off: %q localOnly = %v", name, localOnly)
	}
}

func TestNullRepositoryMemoryInitializesMap(t *testing.T) {
	t.Cleanup(func() { UseRepositoryMemory("") })
	path := filepath.Join(t.TempDir(), "repositories.json")
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}

	UseRepositoryMemory(path)
	RememberRepository("/worktree", "github.com/acme/coslash")
	if got := RememberedRepository("/worktree"); got != "github.com/acme/coslash" {
		t.Fatalf("remembered repository = %q", got)
	}
}
