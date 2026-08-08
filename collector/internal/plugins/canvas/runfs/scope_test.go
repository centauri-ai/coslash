package runfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newTestScope(t *testing.T, options ScopeOptions) (*Scope, string) {
	t.Helper()
	root := t.TempDir()
	scope, err := OpenScope(root, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Close(); err != nil {
			t.Errorf("close scope: %v", err)
		}
	})
	return scope, root
}

func TestScopeCanonicalAtomicWriteAndModes(t *testing.T) {
	container := t.TempDir()
	realRoot := filepath.Join(container, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(container, "link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	scope, err := OpenScope(link, ScopeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Root() != canonicalRoot {
		t.Fatalf("Root() = %q, want %q", scope.Root(), canonicalRoot)
	}

	if err := scope.AtomicWrite(context.Background(), "runs/one/state.json", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := scope.AtomicWrite(context.Background(), "runs/one/state.json", []byte("second")); err != nil {
		t.Fatal(err)
	}
	contents, err := scope.ReadFile(context.Background(), "runs/one/state.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "second" {
		t.Fatalf("contents = %q", contents)
	}
	fileInfo, err := os.Stat(filepath.Join(realRoot, "runs", "one", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o", got)
	}
	dirInfo, err := os.Stat(filepath.Join(realRoot, "runs", "one"))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory mode = %o", got)
	}
}

func TestScopeRejectsTraversalVariablesAndGlobs(t *testing.T) {
	scope, _ := newTestScope(t, ScopeOptions{})
	for _, name := range []string{"../escape", "/absolute", "$ROOT/file", "*.json", "a/?", "a/[x]"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if _, err := scope.Resolve(name); !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("Resolve(%q) error = %v", name, err)
			}
		})
	}
}

func TestScopeRejectsSymlinkedParentAndFile(t *testing.T) {
	scope, root := newTestScope(t, ScopeOptions{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked-parent")); err != nil {
		t.Fatal(err)
	}
	if err := scope.AtomicWrite(context.Background(), "linked-parent/escaped", []byte("no")); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked parent error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside target was touched: %v", err)
	}

	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "linked-file")); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.ReadFile(context.Background(), "linked-file"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked read error = %v", err)
	}
	if err := scope.AtomicWrite(context.Background(), "linked-file", []byte("overwrite")); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked write error = %v", err)
	}
	contents, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "secret" {
		t.Fatalf("secret changed to %q", contents)
	}
}

func TestScopeBoundsReadsAndWrites(t *testing.T) {
	scope, root := newTestScope(t, ScopeOptions{MaxReadBytes: 4, MaxWriteBytes: 4})
	if err := scope.AtomicWrite(context.Background(), "small", []byte("1234")); err != nil {
		t.Fatal(err)
	}
	if err := scope.AtomicWrite(context.Background(), "large", []byte("12345")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large write error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "external-large"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.ReadFile(context.Background(), "external-large"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large read error = %v", err)
	}
}

func TestAtomicWriteFailureStagesNeverExposePartialContent(t *testing.T) {
	scope, root := newTestScope(t, ScopeOptions{})
	ctx := context.Background()
	if err := scope.AtomicWrite(ctx, "state.json", []byte("old")); err != nil {
		t.Fatal(err)
	}
	crash := errors.New("simulated crash")
	for _, stage := range []string{"written", "file_synced"} {
		t.Run(stage, func(t *testing.T) {
			err := scope.atomicWrite(ctx, "state.json", []byte("new"), func(current string) error {
				if current == stage {
					return crash
				}
				return nil
			})
			if !errors.Is(err, crash) {
				t.Fatalf("error = %v", err)
			}
			contents, err := os.ReadFile(filepath.Join(root, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != "old" {
				t.Fatalf("partial replacement became visible: %q", contents)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".tmp") {
					t.Fatalf("temporary file leaked: %s", entry.Name())
				}
			}
		})
	}

	err := scope.atomicWrite(ctx, "state.json", []byte("renamed"), func(stage string) error {
		if stage == "renamed" {
			return crash
		}
		return nil
	})
	if !errors.Is(err, crash) {
		t.Fatalf("post-rename error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "renamed" {
		t.Fatalf("post-rename content = %q", contents)
	}
}

func TestScopeHonorsCancellation(t *testing.T) {
	scope, root := newTestScope(t, ScopeOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scope.AtomicWrite(ctx, "canceled", []byte("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "canceled")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled write touched disk: %v", err)
	}
}

func TestScopeRejectsNonDirectoryRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenScope(file, ScopeOptions{}); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("error = %v", err)
	}
}

func TestScopeModesArePortablePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}
	root := t.TempDir()
	if _, err := OpenScope(root, ScopeOptions{FileMode: os.ModeDir | 0o600}); err == nil {
		t.Fatal("accepted type bits in file mode")
	}
}
