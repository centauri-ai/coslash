package revision

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

func newGit(t *testing.T) *Git {
	t.Helper()
	git, err := NewGit(NewExecRunner(), realPath(t, t.TempDir()))
	if err != nil {
		t.Fatalf("NewGit: %v", err)
	}
	return git
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}

// rawGit runs setup commands outside the hardened wrapper.
func rawGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@localhost",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@localhost",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// newRepository builds a repository with one commit on main.
func newRepository(t *testing.T) string {
	t.Helper()
	dir := realPath(t, t.TempDir())
	rawGit(t, dir, "init", "-b", "main", "--quiet")
	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	rawGit(t, dir, "add", "README.md")
	rawGit(t, dir, "commit", "--quiet", "-m", "initial")
	return dir
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *Error, got %v", err)
	}
	return typed.Code
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

func TestPreflightResolvesCheckedOutBranch(t *testing.T) {
	repository := newRepository(t)
	git := newGit(t)

	ready, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: repository})
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if ready.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want main", ready.BaseBranch)
	}
	if !ValidObjectID(ready.BaseSha) {
		t.Fatalf("BaseSha = %q, want a full object name", ready.BaseSha)
	}
	if !ready.IsGitRepository {
		t.Fatal("IsGitRepository = false, want true")
	}
	if ready.Toplevel != repository {
		t.Fatalf("Toplevel = %q, want the realpath %q", ready.Toplevel, repository)
	}
}

func TestPreflightRefusesPlainFolderUnlessAllowed(t *testing.T) {
	folder := realPath(t, t.TempDir())
	git := newGit(t)

	_, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: folder})
	if got := codeOf(t, err); got != CodeNotARepository {
		t.Fatalf("code = %q, want %q", got, CodeNotARepository)
	}

	ready, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: folder, AllowPlainFolder: true})
	if err != nil {
		t.Fatalf("RunPreflight(AllowPlainFolder): %v", err)
	}
	if ready.IsGitRepository {
		t.Fatal("IsGitRepository = true for a plain folder")
	}
}

func TestPreflightRefusesBareRepository(t *testing.T) {
	dir := realPath(t, t.TempDir())
	rawGit(t, dir, "init", "--bare", "-b", "main", "--quiet")
	git := newGit(t)

	_, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: dir})
	if got := codeOf(t, err); got != CodeBareRepository {
		t.Fatalf("code = %q, want %q", got, CodeBareRepository)
	}
}

func TestPreflightRefusesOperationInProgress(t *testing.T) {
	repository := newRepository(t)
	writeFile(t, filepath.Join(repository, ".git", "MERGE_HEAD"), "deadbeef\n")
	git := newGit(t)

	_, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: repository})
	if got := codeOf(t, err); got != CodeRepositoryBusy {
		t.Fatalf("code = %q, want %q", got, CodeRepositoryBusy)
	}
}

func TestPreflightRefusesDetachedHeadWithoutOriginHead(t *testing.T) {
	repository := newRepository(t)
	head := rawGit(t, repository, "rev-parse", "HEAD")
	rawGit(t, repository, "checkout", "--quiet", "--detach", head)
	git := newGit(t)

	_, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: repository})
	if got := codeOf(t, err); got != CodeAmbiguousBase {
		t.Fatalf("code = %q, want %q", got, CodeAmbiguousBase)
	}
}

func TestPreflightRefusesMissingBaseBranch(t *testing.T) {
	repository := newRepository(t)
	git := newGit(t)

	_, err := git.RunPreflight(t.Context(), PreflightOptions{
		ProjectPath: repository,
		BaseBranch:  "does-not-exist",
	})
	if got := codeOf(t, err); got != CodeBaseNotFound {
		t.Fatalf("code = %q, want %q", got, CodeBaseNotFound)
	}
}

func TestPreflightPrefersLinkedWorktreeBranchOverDefault(t *testing.T) {
	repository := newRepository(t)
	rawGit(t, repository, "branch", "feature")
	linked := filepath.Join(realPath(t, t.TempDir()), "linked")
	rawGit(t, repository, "worktree", "add", "--quiet", linked, "feature")
	git := newGit(t)

	ready, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: realPath(t, linked)})
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if ready.BaseBranch != "feature" {
		t.Fatalf("BaseBranch = %q, want feature (the worktree tip, not the repository default)", ready.BaseBranch)
	}
	if !ready.IsLinkedWorktree {
		t.Fatal("IsLinkedWorktree = false for a linked worktree")
	}
}

func TestPreflightDropsUnsafeRemote(t *testing.T) {
	repository := newRepository(t)
	rawGit(t, repository, "remote", "add", "origin", "ext::sh -c 'curl evil'")
	git := newGit(t)

	ready, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: repository})
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if ready.RemoteURL != "" {
		t.Fatalf("RemoteURL = %q, want it dropped", ready.RemoteURL)
	}
}

// ---------------------------------------------------------------------------
// Run roots
// ---------------------------------------------------------------------------

// repositorySnapshot captures the user-visible state that a run must not touch.
type repositorySnapshot struct {
	status string
	refs   string
	index  []byte
	head   string
}

func snapshotRepository(t *testing.T, repository string) repositorySnapshot {
	t.Helper()
	index, err := os.ReadFile(filepath.Join(repository, ".git", "index"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read index: %v", err)
	}
	return repositorySnapshot{
		status: rawGit(t, repository, "status", "--porcelain"),
		refs:   rawGit(t, repository, "show-ref"),
		index:  index,
		head:   rawGit(t, repository, "rev-parse", "HEAD"),
	}
}

func (s repositorySnapshot) assertUnchanged(t *testing.T, repository string) {
	t.Helper()
	after := snapshotRepository(t, repository)
	if after.status != s.status {
		t.Errorf("status changed:\nbefore %q\nafter  %q", s.status, after.status)
	}
	if after.refs != s.refs {
		t.Errorf("refs changed:\nbefore %q\nafter  %q", s.refs, after.refs)
	}
	if after.head != s.head {
		t.Errorf("HEAD changed: before %q after %q", s.head, after.head)
	}
	if string(after.index) != string(s.index) {
		t.Error("the user index is no longer byte-identical")
	}
}

func TestCreateRunRootIsolatesTheUserRepository(t *testing.T) {
	repository := newRepository(t)
	git := newGit(t)
	before := snapshotRepository(t, repository)

	ready, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: repository})
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	runRoot := filepath.Join(realPath(t, t.TempDir()), "roots", "run-1")
	created, err := git.CreateRunRoot(t.Context(), CreateRunRootOptions{
		Preflight: ready,
		Path:      runRoot,
		Branch:    "canvas/run-1",
	})
	if err != nil {
		t.Fatalf("CreateRunRoot: %v", err)
	}
	if created.InPlace {
		t.Fatal("a cloned run root reported InPlace")
	}
	if created.BaseSha != ready.BaseSha {
		t.Fatalf("BaseSha = %q, want %q", created.BaseSha, ready.BaseSha)
	}

	// The clone must own its object store: a hardlinked object would let an
	// agent corrupt the user's repository through a shared inode.
	sourceObject := firstLooseObject(t, filepath.Join(repository, ".git", "objects"))
	cloneObject := firstLooseObject(t, filepath.Join(runRoot, ".git", "objects"))
	if sourceObject != "" && cloneObject != "" && sameInode(t, sourceObject, cloneObject) {
		t.Error("clone shares an object inode with the source repository")
	}

	before.assertUnchanged(t, repository)
}

func firstLooseObject(t *testing.T, objects string) string {
	t.Helper()
	var found string
	_ = filepath.Walk(objects, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || found != "" {
			return nil //nolint:nilerr // a missing object directory is not a test failure
		}
		if strings.Contains(path, "/pack/") || strings.Contains(path, "/info/") {
			return nil
		}
		found = path
		return nil
	})
	return found
}

func sameInode(t *testing.T, left, right string) bool {
	t.Helper()
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

func TestCreateRunRootRefusesExistingPathAndBadBranch(t *testing.T) {
	repository := newRepository(t)
	git := newGit(t)
	ready, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: repository})
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}

	existing := filepath.Join(realPath(t, t.TempDir()), "roots", "taken")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	_, err = git.CreateRunRoot(t.Context(), CreateRunRootOptions{
		Preflight: ready, Path: existing, Branch: "canvas/run",
	})
	if got := codeOf(t, err); got != CodeRunRootExists {
		t.Fatalf("code = %q, want %q", got, CodeRunRootExists)
	}

	_, err = git.CreateRunRoot(t.Context(), CreateRunRootOptions{
		Preflight: ready,
		Path:      filepath.Join(realPath(t, t.TempDir()), "roots", "fresh"),
		Branch:    "../escape",
	})
	if got := codeOf(t, err); got != CodeInvalidBranch {
		t.Fatalf("code = %q, want %q", got, CodeInvalidBranch)
	}
}

func TestExchangeDirectoryIsIgnored(t *testing.T) {
	repository := newRepository(t)
	git := newGit(t)

	if err := git.WriteExchangeDirectory(t.Context(), repository); err != nil {
		t.Fatalf("WriteExchangeDirectory: %v", err)
	}
	// The whole tree, including the .gitignore itself, must disappear from status.
	if status := rawGit(t, repository, "status", "--porcelain"); status != "" {
		t.Fatalf("status = %q, want empty after the exchange directory is written", status)
	}
}

func TestAttachInPlaceRefusesDirtyWorktreeAndReusesWorkBranch(t *testing.T) {
	repository := newRepository(t)
	rawGit(t, repository, "branch", "canvas-work")
	git := newGit(t)

	ready, err := git.RunPreflight(t.Context(), PreflightOptions{
		ProjectPath: repository, BaseBranch: "canvas-work",
	})
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}

	writeFile(t, filepath.Join(repository, "dirty.txt"), "uncommitted\n")
	_, err = git.AttachInPlaceRunRoot(t.Context(), ready)
	if got := codeOf(t, err); got != CodeDirtyWorktree {
		t.Fatalf("code = %q, want %q", got, CodeDirtyWorktree)
	}

	if err := os.Remove(filepath.Join(repository, "dirty.txt")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	attached, err := git.AttachInPlaceRunRoot(t.Context(), ready)
	if err != nil {
		t.Fatalf("AttachInPlaceRunRoot: %v", err)
	}
	if !attached.InPlace {
		t.Fatal("InPlace = false for an in-place attach")
	}
	if attached.Branch != "canvas-work" {
		t.Fatalf("Branch = %q, want canvas-work", attached.Branch)
	}
	if attached.Path != repository {
		t.Fatalf("Path = %q, want the project folder %q", attached.Path, repository)
	}
}

func TestRemoveRunRootBounds(t *testing.T) {
	base := realPath(t, t.TempDir())

	// Not under a `roots` parent: refused even though it is a repository.
	loose := filepath.Join(base, "loose")
	rawGit(t, base, "init", "-b", "main", "--quiet", loose)
	if err := RemoveRunRoot(loose); codeOf(t, err) != CodeInvalidPath {
		t.Fatalf("expected refusal for a root outside a roots directory, got %v", err)
	}
	if _, err := os.Stat(loose); err != nil {
		t.Fatal("the refused path was removed anyway")
	}

	// Under `roots` but not a repository: still refused.
	notRepository := filepath.Join(base, "roots", "plain")
	if err := os.MkdirAll(notRepository, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := RemoveRunRoot(notRepository); codeOf(t, err) != CodeInvalidPath {
		t.Fatalf("expected refusal for a non-repository, got %v", err)
	}

	// Both guards satisfied.
	disposable := filepath.Join(base, "roots", "run-9")
	rawGit(t, base, "init", "-b", "main", "--quiet", disposable)
	if err := RemoveRunRoot(disposable); err != nil {
		t.Fatalf("RemoveRunRoot: %v", err)
	}
	if _, err := os.Stat(disposable); !os.IsNotExist(err) {
		t.Fatal("the disposable run root survived removal")
	}

	// A missing path is not an error, so teardown is idempotent.
	if err := RemoveRunRoot(disposable); err != nil {
		t.Fatalf("RemoveRunRoot on a missing path: %v", err)
	}
	if err := RemoveRunRoot("relative/roots/run"); codeOf(t, err) != CodeInvalidPath {
		t.Fatal("a relative run root was not refused")
	}
}

// ---------------------------------------------------------------------------
// Capture
// ---------------------------------------------------------------------------

func newRunRoot(t *testing.T) (*Git, string, Preflight) {
	t.Helper()
	repository := newRepository(t)
	git := newGit(t)
	ready, err := git.RunPreflight(t.Context(), PreflightOptions{ProjectPath: repository})
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	runRoot := filepath.Join(realPath(t, t.TempDir()), "roots", "run-1")
	if _, err := git.CreateRunRoot(t.Context(), CreateRunRootOptions{
		Preflight: ready, Path: runRoot, Branch: "canvas/run-1",
	}); err != nil {
		t.Fatalf("CreateRunRoot: %v", err)
	}
	return git, runRoot, ready
}

func TestCaptureRevisionRecordsChangeAndAnchorsTree(t *testing.T) {
	git, runRoot, ready := newRunRoot(t)
	writeFile(t, filepath.Join(runRoot, "added.txt"), "new file\n")
	writeFile(t, filepath.Join(runRoot, "README.md"), "hello\nworld\n")

	indexFile := filepath.Join(realPath(t, t.TempDir()), "capture", "index")
	captured, err := git.CaptureRevision(t.Context(), CaptureRevisionOptions{
		CaptureOptions: CaptureOptions{RunRoot: runRoot, IndexFile: indexFile},
		RunID:          "run-1",
		ChangeRevision: 1,
		BaseSha:        ready.BaseSha,
		RefNamespace:   "canvas",
	})
	if err != nil {
		t.Fatalf("CaptureRevision: %v", err)
	}
	if !ValidObjectID(captured.TreeOID) {
		t.Fatalf("TreeOID = %q", captured.TreeOID)
	}
	if len(captured.ChangedFiles) != 2 {
		t.Fatalf("ChangedFiles = %+v, want 2 entries", captured.ChangedFiles)
	}
	if captured.Insertions != 2 || captured.Deletions != 0 {
		t.Fatalf("insertions/deletions = %d/%d, want 2/0", captured.Insertions, captured.Deletions)
	}
	statuses := map[string]FileStatus{}
	for _, file := range captured.ChangedFiles {
		statuses[file.Path] = file.Status
	}
	if statuses["added.txt"] != StatusAdded {
		t.Fatalf("added.txt status = %q, want A", statuses["added.txt"])
	}
	if statuses["README.md"] != StatusModified {
		t.Fatalf("README.md status = %q, want M", statuses["README.md"])
	}
	if len(captured.PatchSha256) != 64 || captured.PatchBytes != int64(len(captured.Patch)) {
		t.Fatalf("patch digest/bytes inconsistent: %q %d", captured.PatchSha256, captured.PatchBytes)
	}

	// The anchoring ref keeps the tree alive across gc.
	anchored := rawGit(t, runRoot, "rev-parse", "--verify", "refs/canvas/runs/run-1/rev/1^{tree}")
	if anchored != captured.TreeOID {
		t.Fatalf("anchored tree = %q, want %q", anchored, captured.TreeOID)
	}

	// The scratch index is cleaned up and the run root's own index is untouched.
	if _, err := os.Stat(indexFile); !os.IsNotExist(err) {
		t.Error("the scratch capture index survived the capture")
	}
	if status := rawGit(t, runRoot, "status", "--porcelain"); !strings.Contains(status, "added.txt") {
		t.Fatalf("status = %q, want the working change still unstaged", status)
	}
}

func TestCapturePreservesAConcurrentAgentIndex(t *testing.T) {
	git, runRoot, _ := newRunRoot(t)
	writeFile(t, filepath.Join(runRoot, "staged.txt"), "staged\n")
	rawGit(t, runRoot, "add", "staged.txt")
	writeFile(t, filepath.Join(runRoot, "unstaged.txt"), "unstaged\n")

	before, err := os.ReadFile(filepath.Join(runRoot, ".git", "index"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	if _, err := git.CaptureTreeOID(t.Context(), CaptureOptions{
		RunRoot:   runRoot,
		IndexFile: filepath.Join(realPath(t, t.TempDir()), "index"),
	}); err != nil {
		t.Fatalf("CaptureTreeOID: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(runRoot, ".git", "index"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("capture flattened the agent's staged/unstaged split")
	}
}

func TestCaptureExcludesFinderMetadataAndOptionallyTheControlPlane(t *testing.T) {
	git, runRoot, _ := newRunRoot(t)
	indexDirectory := realPath(t, t.TempDir())

	baseline, err := git.CaptureTreeOID(t.Context(), CaptureOptions{
		RunRoot: runRoot, IndexFile: filepath.Join(indexDirectory, "a"),
	})
	if err != nil {
		t.Fatalf("CaptureTreeOID: %v", err)
	}

	// Finder metadata must not move revision identity: it would invalidate an
	// approval nobody edited.
	writeFile(t, filepath.Join(runRoot, ".DS_Store"), "finder\n")
	writeFile(t, filepath.Join(runRoot, "nested", ".DS_Store"), "finder\n")
	withMetadata, err := git.CaptureTreeOID(t.Context(), CaptureOptions{
		RunRoot: runRoot, IndexFile: filepath.Join(indexDirectory, "b"),
	})
	if err != nil {
		t.Fatalf("CaptureTreeOID: %v", err)
	}
	if withMetadata != baseline {
		t.Fatal("Finder metadata changed the revision identity")
	}

	// A control-plane write is a mutation for an ordinary capture but not for
	// the review guard, which excludes the exchange directory.
	writeFile(t, filepath.Join(runRoot, ".fleetlog", "run", "note.txt"), "seat output\n")
	guarded, err := git.CaptureTreeOID(t.Context(), CaptureOptions{
		RunRoot:              runRoot,
		IndexFile:            filepath.Join(indexDirectory, "c"),
		ExcludeExchangePaths: true,
	})
	if err != nil {
		t.Fatalf("CaptureTreeOID(ExcludeExchangePaths): %v", err)
	}
	if guarded != baseline {
		t.Fatal("a control-plane write moved the guarded revision identity")
	}
}

func TestCaptureRevisionHonorsExchangePathExclusion(t *testing.T) {
	git, runRoot, ready := newRunRoot(t)
	indexFile := filepath.Join(realPath(t, t.TempDir()), "capture", "index")
	if err := os.MkdirAll(filepath.Join(runRoot, ".fleetlog", "run", "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runRoot, ".fleetlog", "run", "out", "private.txt"), []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runRoot, "visible.txt"), []byte("visible\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, err := git.CaptureRevision(t.Context(), CaptureRevisionOptions{
		CaptureOptions: CaptureOptions{RunRoot: runRoot, IndexFile: indexFile, ExcludeExchangePaths: true},
		RunID:          "run-1", ChangeRevision: 1, BaseSha: ready.BaseSha, RefNamespace: "dagama",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.ChangedFiles) != 1 || captured.ChangedFiles[0].Path != "visible.txt" || strings.Contains(string(captured.Patch), ".fleetlog/run") {
		t.Fatalf("exchange paths leaked into revision: files=%#v patch=%q", captured.ChangedFiles, captured.Patch)
	}
}

func TestCaptureRefusesAnIndexInsideTheRunRoot(t *testing.T) {
	git, runRoot, _ := newRunRoot(t)

	_, err := git.CaptureTreeOID(t.Context(), CaptureOptions{
		RunRoot:   runRoot,
		IndexFile: filepath.Join(runRoot, "capture-index"),
	})
	if got := codeOf(t, err); got != CodeInvalidPath {
		t.Fatalf("code = %q, want %q", got, CodeInvalidPath)
	}
}

func TestCaptureRefusesUnsafeRefComponents(t *testing.T) {
	git, runRoot, ready := newRunRoot(t)

	for _, runID := range []string{"../escape", "run 1", "-dash", ""} {
		_, err := git.CaptureRevision(t.Context(), CaptureRevisionOptions{
			CaptureOptions: CaptureOptions{
				RunRoot:   runRoot,
				IndexFile: filepath.Join(realPath(t, t.TempDir()), "index"),
			},
			RunID:          runID,
			ChangeRevision: 1,
			BaseSha:        ready.BaseSha,
			RefNamespace:   "canvas",
		})
		if err == nil {
			t.Fatalf("run id %q was accepted", runID)
		}
	}
}

func TestConcurrentTreeCapturesAgree(t *testing.T) {
	git, runRoot, _ := newRunRoot(t)
	writeFile(t, filepath.Join(runRoot, "shared.txt"), "content\n")
	indexDirectory := realPath(t, t.TempDir())

	const workers = 8
	results := make([]string, workers)
	failures := make([]error, workers)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			oid, err := git.CaptureTreeOID(context.Background(), CaptureOptions{
				RunRoot:   runRoot,
				IndexFile: filepath.Join(indexDirectory, "index-"+string(rune('a'+index))),
			})
			results[index] = oid
			failures[index] = err
		}()
	}
	group.Wait()

	for index := range workers {
		if failures[index] != nil {
			t.Fatalf("worker %d: %v", index, failures[index])
		}
		if results[index] != results[0] {
			t.Fatalf("worker %d captured %q, want %q", index, results[index], results[0])
		}
	}
}

func TestCancelledContextStopsCapture(t *testing.T) {
	git, runRoot, _ := newRunRoot(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := git.CaptureTreeOID(ctx, CaptureOptions{
		RunRoot:   runRoot,
		IndexFile: filepath.Join(realPath(t, t.TempDir()), "index"),
	}); err == nil {
		t.Fatal("a cancelled context still captured a tree")
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func TestValidBranchName(t *testing.T) {
	valid := []string{"main", "canvas/run-1", "feature.x", "a"}
	for _, name := range valid {
		if !ValidBranchName(name) {
			t.Errorf("ValidBranchName(%q) = false, want true", name)
		}
	}
	invalid := []string{"", "-dash", "../escape", "a..b", "with space", "ends/", "x.lock", strings.Repeat("a", 200)}
	for _, name := range invalid {
		if ValidBranchName(name) {
			t.Errorf("ValidBranchName(%q) = true, want false", name)
		}
	}
}

func TestValidateRemoteURL(t *testing.T) {
	accepted := []string{
		"https://github.com/owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"git@github.com:owner/repo.git",
	}
	for _, remote := range accepted {
		if err := ValidateRemoteURL(remote); err != nil {
			t.Errorf("ValidateRemoteURL(%q) = %v, want nil", remote, err)
		}
	}

	// ext:: and fd:: execute a command named in the URL; file:// and relative
	// traversal reach outside the intended transports.
	refused := []string{
		"", "ext::sh -c 'curl evil'", "fd::7", "file:///etc/passwd",
		"/local/path/repo.git", "git://github.com/owner/repo.git",
		"-upload-pack=evil", "https://github.com/../../etc",
		"https://github.com/owner/repo\nrm -rf /",
	}
	for _, remote := range refused {
		if err := ValidateRemoteURL(remote); err == nil {
			t.Errorf("ValidateRemoteURL(%q) = nil, want a refusal", remote)
		}
	}
}

func TestErrorMessagesWithholdCommandOutput(t *testing.T) {
	git := newGit(t)
	// A command that fails inside a directory that is not a repository.
	_, err := git.Output(t.Context(), Command{
		Args: []string{"-C", realPath(t, t.TempDir()), "rev-parse", "HEAD"},
	})
	if err == nil {
		t.Fatal("expected a failure")
	}
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *Error, got %v", err)
	}
	if strings.Contains(typed.Message, "fatal:") || strings.Contains(typed.Error(), "fatal:") {
		t.Fatalf("client-facing message leaked git stderr: %q", typed.Error())
	}
	if typed.Detail() == "" {
		t.Fatal("Detail() is empty, so the diagnostic was lost rather than withheld")
	}
	if !errors.Is(err, ErrGit) {
		t.Fatal("errors.Is(err, ErrGit) = false")
	}
}

func TestHooksDirectoryMustBeEmpty(t *testing.T) {
	directory := realPath(t, t.TempDir())
	writeFile(t, filepath.Join(directory, "pre-commit"), "#!/bin/sh\n")

	// A non-empty hooks directory would silently execute whatever it contains
	// on every controller commit.
	if _, err := NewGit(NewExecRunner(), directory); err == nil {
		t.Fatal("a non-empty hooks directory was accepted")
	}
	if _, err := NewGit(NewExecRunner(), "relative/path"); err == nil {
		t.Fatal("a relative hooks directory was accepted")
	}
}

func TestAgentControlledLocalConfigCannotExecuteDuringCapture(t *testing.T) {
	git, runRoot, _ := newRunRoot(t)
	sentinel := filepath.Join(realPath(t, t.TempDir()), "filter-ran")
	rawGit(t, runRoot, "config", "--local", "filter.evil.clean", "touch "+sentinel)
	writeFile(t, filepath.Join(runRoot, ".gitattributes"), "*.txt filter=evil\n")
	writeFile(t, filepath.Join(runRoot, "payload.txt"), "payload\n")

	_, err := git.CaptureTreeOID(t.Context(), CaptureOptions{
		RunRoot: runRoot, IndexFile: filepath.Join(realPath(t, t.TempDir()), "index"),
	})
	if got := codeOf(t, err); got != CodeGitFailed {
		t.Fatalf("code = %q, want %q", got, CodeGitFailed)
	}
	if _, statErr := os.Stat(sentinel); !os.IsNotExist(statErr) {
		t.Fatalf("agent-controlled clean filter executed: %v", statErr)
	}
}

func TestUnsafeLocalConfigClassification(t *testing.T) {
	unsafe := map[string]string{
		"filter.evil.clean":           "touch /tmp/pwned",
		"core.sshcommand":             "helper",
		"credential.helper":           "helper",
		"url.https://evil/.insteadof": "https://github.com/",
		"http.proxy":                  "https://evil.invalid",
		"include.path":                "/tmp/agent-config",
		"diff.agent.textconv":         "helper",
		"remote.origin.receivepack":   "helper",
		"submodule.payload.update":    "!helper",
		"commit.gpgsign":              "true",
		"interactive.difffilter":      "helper",
		"core.worktree":               "/unapproved/tree",
	}
	for key, value := range unsafe {
		if !unsafeLocalConfig(key, value) {
			t.Errorf("%s was not classified as unsafe", key)
		}
	}
	for key, value := range map[string]string{
		"core.repositoryformatversion": "0",
		"core.filemode":                "true",
		"core.bare":                    "false",
		"remote.origin.url":            "https://github.com/owner/repo.git",
		"branch.main.merge":            "refs/heads/main",
	} {
		if unsafeLocalConfig(key, value) {
			t.Errorf("%s was classified as unsafe", key)
		}
	}
}

func TestBoundedOutputRefusesOversizedResult(t *testing.T) {
	git, runRoot, _ := newRunRoot(t)
	// A tracked file, so the change reaches `git diff HEAD` without staging.
	writeFile(t, filepath.Join(runRoot, "README.md"), strings.Repeat("x\n", 4096))

	_, err := git.RawOutput(t.Context(), Command{
		Args:           []string{"-C", runRoot, "diff", "--no-color", "HEAD"},
		MaxOutputBytes: 128,
	})
	if err == nil {
		t.Fatal("an oversized result was returned instead of refused")
	}
	if got := codeOf(t, err); got != CodePatchTooLarge {
		t.Fatalf("code = %q, want %q", got, CodePatchTooLarge)
	}
}
