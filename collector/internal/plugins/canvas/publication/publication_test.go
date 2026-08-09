package publication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

const (
	treeOID     = "1111111111111111111111111111111111111111"
	baseSha     = "2222222222222222222222222222222222222222"
	commitSha   = "3333333333333333333333333333333333333333"
	remoteURL   = "https://github.com/owner/repo.git"
	runBranch   = "canvas/run-1"
	baseBranch  = "main"
	patchDigest = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

// ---------------------------------------------------------------------------
// Fakes — no test in this package may contact GitHub or a real remote.
// ---------------------------------------------------------------------------

// fakeGit answers hardened git invocations from a table.
type fakeGit struct {
	mutex sync.Mutex
	calls [][]string

	treeOID    string
	remoteHead string
	treePaths  []string
	headLog    string
	// stagedDirty controls `diff --cached --quiet`: a non-zero exit means
	// something is staged.
	stagedDirty bool
	failures    map[string]revision.Result
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		treeOID:     treeOID,
		remoteHead:  baseSha,
		treePaths:   []string{"src/main.go", "README.md"},
		stagedDirty: true,
		failures:    map[string]revision.Result{},
	}
}

// verb strips the hardening this package always applies so the fake can switch
// on the operation the caller actually asked for.
func verb(args []string) (string, []string) {
	rest := args
	for len(rest) >= 2 && rest[0] == "-c" {
		rest = rest[2:]
	}
	if len(rest) >= 2 && rest[0] == "-C" {
		rest = rest[2:]
	}
	if len(rest) == 0 {
		return "", nil
	}
	return rest[0], rest[1:]
}

func (g *fakeGit) Run(_ context.Context, command revision.Command) (revision.Result, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.calls = append(g.calls, command.Args)

	operation, rest := verb(command.Args)
	if forced, ok := g.failures[operation]; ok {
		return forced, nil
	}

	switch operation {
	case "read-tree", "add", "update-ref", "commit":
		return revision.Result{}, nil
	case "write-tree":
		return revision.Result{Stdout: []byte(g.treeOID + "\n")}, nil
	case "rev-parse":
		return revision.Result{Stdout: []byte(commitSha + "\n")}, nil
	case "log":
		return revision.Result{Stdout: []byte(g.headLog)}, nil
	case "diff":
		if len(rest) > 0 && rest[0] == "--cached" && contains(rest, "--quiet") {
			if g.stagedDirty {
				return revision.Result{ExitCode: 1}, nil
			}
			return revision.Result{}, nil
		}
		return revision.Result{}, nil
	case "ls-remote":
		if g.remoteHead == "" {
			return revision.Result{ExitCode: 2, Stderr: []byte("no such ref")}, nil
		}
		return revision.Result{Stdout: []byte(g.remoteHead + "\trefs/heads/" + baseBranch + "\n")}, nil
	case "ls-tree":
		return revision.Result{Stdout: []byte(strings.Join(g.treePaths, "\n") + "\n")}, nil
	case "push":
		return revision.Result{}, nil
	default:
		return revision.Result{}, nil
	}
}

func (g *fakeGit) called(operation string) int {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	count := 0
	for _, args := range g.calls {
		if found, _ := verb(args); found == operation {
			count++
		}
	}
	return count
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// fakeGitHub answers `gh` invocations from a table.
type fakeGitHub struct {
	mutex sync.Mutex
	calls [][]string

	// existing is returned by `pr list`; empty means no pull request yet.
	existing  string
	createOut string
	viewOut   string
	failures  map[string]GitHubResult
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{
		createOut: "https://github.com/owner/repo/pull/42\n",
		viewOut:   `{"number":42,"url":"https://github.com/owner/repo/pull/42"}`,
		failures:  map[string]GitHubResult{},
	}
}

func (h *fakeGitHub) Run(_ context.Context, args []string, _ string) (GitHubResult, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.calls = append(h.calls, args)

	action := ""
	if len(args) >= 2 {
		action = args[0] + " " + args[1]
	}
	if forced, ok := h.failures[action]; ok {
		return forced, nil
	}
	switch action {
	case "pr list":
		return GitHubResult{Stdout: []byte(h.existing)}, nil
	case "pr create":
		// Once created, a later list must find it — this is what makes a retry
		// update rather than open a second pull request.
		h.existing = h.viewOut
		return GitHubResult{Stdout: []byte(h.createOut)}, nil
	case "pr view":
		return GitHubResult{Stdout: []byte(h.viewOut)}, nil
	case "pr edit":
		return GitHubResult{}, nil
	default:
		return GitHubResult{}, nil
	}
}

func (h *fakeGitHub) called(action string) int {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	count := 0
	for _, args := range h.calls {
		if len(args) >= 2 && args[0]+" "+args[1] == action {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

func newPublisher(t *testing.T, git *fakeGit, github *fakeGitHub) *Publisher {
	t.Helper()
	hooks, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	wrapped, err := revision.NewGit(git, hooks)
	if err != nil {
		t.Fatalf("NewGit: %v", err)
	}
	fixed := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	publisher, err := NewPublisher(wrapped, github, func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	return publisher
}

func newRequest(t *testing.T) Request {
	t.Helper()
	runRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	index, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return Request{
		RunID:      "run-1",
		RunRoot:    runRoot,
		Branch:     runBranch,
		BaseBranch: baseBranch,
		BaseSha:    baseSha,
		RemoteURL:  remoteURL,
		Revision: revision.Revision{
			ChangeRevision: 3,
			TreeOID:        treeOID,
			PatchSha256:    patchDigest,
			ChangedFiles:   []revision.ChangedFile{{Path: "src/main.go", Status: revision.StatusModified}},
		},
		Review: ReviewFact{Approved: true, ChangeRevision: 3},
		Verification: &verification.Document{
			ChangeRevision: 3,
			Verdict:        verification.VerdictPassed,
		},
		CaptureIndexFile: filepath.Join(index, "capture-index"),
	}
}

func itemByID(result PreflightResult, id string) ChecklistItem {
	for _, item := range result.Checklist {
		if item.ID == id {
			return item
		}
	}
	return ChecklistItem{ID: id, Label: "missing"}
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

func TestPreflightPassesWhenEveryGateIsSatisfied(t *testing.T) {
	publisher := newPublisher(t, newFakeGit(), newFakeGitHub())

	result, err := publisher.Preflight(t.Context(), newRequest(t))
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !result.OK {
		t.Fatalf("Preflight refused: %v", result.FailedItems())
	}
	for _, id := range []string{
		ItemReviewApproved, ItemVerificationOK, ItemRevisionCurrent, ItemBaseUnmoved,
		ItemNoControlPlane, ItemNonEmptyChange, ItemRemoteSafe, ItemRunBranch,
	} {
		if item := itemByID(result, id); !item.OK {
			t.Errorf("gate %s = %+v, want ok", id, item)
		}
	}
}

func TestPreflightGates(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(git *fakeGit, request *Request)
		failing string
	}{
		{
			name:    "review has not approved",
			mutate:  func(_ *fakeGit, r *Request) { r.Review.Approved = false },
			failing: ItemReviewApproved,
		},
		{
			name:    "review approved a different revision",
			mutate:  func(_ *fakeGit, r *Request) { r.Review.ChangeRevision = 2 },
			failing: ItemReviewApproved,
		},
		{
			name:    "verification missing",
			mutate:  func(_ *fakeGit, r *Request) { r.Verification = nil },
			failing: ItemVerificationOK,
		},
		{
			name: "verification failed",
			mutate: func(_ *fakeGit, r *Request) {
				r.Verification.Verdict = verification.VerdictFailed
			},
			failing: ItemVerificationOK,
		},
		{
			name: "verification bound to an older revision",
			mutate: func(_ *fakeGit, r *Request) {
				r.Verification.ChangeRevision = 2
			},
			failing: ItemVerificationOK,
		},
		{
			name:    "worktree mutated after approval",
			mutate:  func(g *fakeGit, _ *Request) { g.treeOID = strings.Repeat("9", 40) },
			failing: ItemRevisionCurrent,
		},
		{
			name:    "base branch moved",
			mutate:  func(g *fakeGit, _ *Request) { g.remoteHead = strings.Repeat("8", 40) },
			failing: ItemBaseUnmoved,
		},
		{
			name:    "base branch unreadable",
			mutate:  func(g *fakeGit, _ *Request) { g.remoteHead = "" },
			failing: ItemBaseUnmoved,
		},
		{
			name: "control plane in the tree",
			mutate: func(g *fakeGit, _ *Request) {
				g.treePaths = append(g.treePaths, ".fleetlog/run/state.json")
			},
			failing: ItemNoControlPlane,
		},
		{
			name: "workflow file in the tree",
			mutate: func(g *fakeGit, _ *Request) {
				g.treePaths = append(g.treePaths, ".github/workflows/release.yml")
			},
			failing: ItemNoControlPlane,
		},
		{
			name:    "empty change",
			mutate:  func(_ *fakeGit, r *Request) { r.Revision.ChangedFiles = nil },
			failing: ItemNonEmptyChange,
		},
		{
			name:    "unsafe remote",
			mutate:  func(_ *fakeGit, r *Request) { r.RemoteURL = "ext::sh -c 'curl evil'" },
			failing: ItemRemoteSafe,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			git := newFakeGit()
			request := newRequest(t)
			test.mutate(git, &request)
			publisher := newPublisher(t, git, newFakeGitHub())

			result, err := publisher.Preflight(t.Context(), request)
			if err != nil {
				t.Fatalf("Preflight: %v", err)
			}
			if result.OK {
				t.Fatal("Preflight passed despite a violated gate")
			}
			if item := itemByID(result, test.failing); item.OK {
				t.Fatalf("gate %s reported ok; failed items were %v", test.failing, result.FailedItems())
			}
		})
	}
}

func TestPreflightExcludesTheControlPlaneFromMutationDetection(t *testing.T) {
	git := newFakeGit()
	publisher := newPublisher(t, git, newFakeGitHub())

	if _, err := publisher.Preflight(t.Context(), newRequest(t)); err != nil {
		t.Fatalf("Preflight: %v", err)
	}

	// Review and verification write their own evidence under the exchange
	// directory; those writes are not project-file mutations.
	var sawExclusion bool
	git.mutex.Lock()
	for _, args := range git.calls {
		if operation, rest := verb(args); operation == "add" && contains(rest, revision.ExcludeExchange) {
			sawExclusion = true
		}
	}
	git.mutex.Unlock()
	if !sawExclusion {
		t.Fatal("mutation detection did not exclude the exchange directory")
	}
}

func TestPreflightRefusesMalformedRequests(t *testing.T) {
	publisher := newPublisher(t, newFakeGit(), newFakeGitHub())

	cases := map[string]func(r *Request){
		"missing run id":    func(r *Request) { r.RunID = "" },
		"missing run root":  func(r *Request) { r.RunRoot = "" },
		"missing index":     func(r *Request) { r.CaptureIndexFile = "" },
		"bad run branch":    func(r *Request) { r.Branch = "../escape" },
		"bad base branch":   func(r *Request) { r.BaseBranch = "-flag" },
		"short base sha":    func(r *Request) { r.BaseSha = "abc" },
		"short tree object": func(r *Request) { r.Revision.TreeOID = "abc" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := newRequest(t)
			mutate(&request)
			if _, err := publisher.Preflight(t.Context(), request); err == nil {
				t.Fatal("a malformed request was accepted")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Execute and idempotency
// ---------------------------------------------------------------------------

func TestExecuteCreatesOnePullRequest(t *testing.T) {
	git := newFakeGit()
	github := newFakeGitHub()
	publisher := newPublisher(t, git, github)

	record, err := publisher.Execute(t.Context(), newRequest(t), "Add the thing", "body")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if record.Action != ActionCreated {
		t.Fatalf("Action = %q, want %q", record.Action, ActionCreated)
	}
	if record.PRNumber != 42 || record.PRURL == "" {
		t.Fatalf("record = %+v, want the created pull request identity", record)
	}
	if record.IdempotencyKey != "run-1:3" {
		t.Fatalf("IdempotencyKey = %q, want run-1:3", record.IdempotencyKey)
	}
	if record.CommitSha != commitSha {
		t.Fatalf("CommitSha = %q", record.CommitSha)
	}
	if github.called("pr create") != 1 {
		t.Fatalf("pr create ran %d times, want 1", github.called("pr create"))
	}
	if git.called("push") != 1 {
		t.Fatalf("push ran %d times, want 1", git.called("push"))
	}
}

func TestExecuteRetryUpdatesInsteadOfOpeningASecondPullRequest(t *testing.T) {
	git := newFakeGit()
	github := newFakeGitHub()
	publisher := newPublisher(t, git, github)
	request := newRequest(t)

	first, err := publisher.Execute(t.Context(), request, "Add the thing", "body")
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	// The retry sees the commit it already made: HEAD carries the key, so the
	// commit is skipped and the existing pull request is updated.
	git.headLog = commitSha + "\nAdd the thing\n\nChange revision 3.\nIdempotency-Key: " +
		IdempotencyKey(request.RunID, request.Revision.ChangeRevision) + "\n"

	second, err := publisher.Execute(t.Context(), request, "Add the thing", "body")
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if second.Action != ActionUpdated {
		t.Fatalf("Action = %q, want %q", second.Action, ActionUpdated)
	}
	if second.IdempotencyKey != first.IdempotencyKey {
		t.Fatal("the retry used a different idempotency key")
	}
	if second.PRNumber != first.PRNumber {
		t.Fatalf("PRNumber changed on retry: %d then %d", first.PRNumber, second.PRNumber)
	}
	if got := github.called("pr create"); got != 1 {
		t.Fatalf("pr create ran %d times across two Executes, want exactly 1", got)
	}
	if got := github.called("pr edit"); got != 1 {
		t.Fatalf("pr edit ran %d times, want 1", got)
	}
	// The second run must not have produced another commit.
	if got := git.called("commit"); got != 1 {
		t.Fatalf("commit ran %d times across two Executes, want exactly 1", got)
	}
}

func TestExecuteUpdatesAPullRequestOpenedEarlier(t *testing.T) {
	github := newFakeGitHub()
	github.existing = `[{"number":7,"url":"https://github.com/owner/repo/pull/7"}]`
	publisher := newPublisher(t, newFakeGit(), github)

	record, err := publisher.Execute(t.Context(), newRequest(t), "Add the thing", "body")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if record.Action != ActionUpdated || record.PRNumber != 7 {
		t.Fatalf("record = %+v, want an update of pull request 7", record)
	}
	if github.called("pr create") != 0 {
		t.Fatal("a pull request was created despite an existing one")
	}
}

func TestExecuteRefusesAnEmptyStagedChange(t *testing.T) {
	git := newFakeGit()
	git.stagedDirty = false
	publisher := newPublisher(t, git, newFakeGitHub())

	_, err := publisher.Execute(t.Context(), newRequest(t), "Add the thing", "body")
	if got := codeOf(t, err); got != CodeEmptyChange {
		t.Fatalf("code = %q, want %q", got, CodeEmptyChange)
	}
	if git.called("push") != 0 {
		t.Fatal("an empty change was pushed")
	}
}

func TestExecuteStopsAtAFailedPreflight(t *testing.T) {
	git := newFakeGit()
	git.treeOID = strings.Repeat("9", 40)
	github := newFakeGitHub()
	publisher := newPublisher(t, git, github)

	_, err := publisher.Execute(t.Context(), newRequest(t), "Add the thing", "body")
	if got := codeOf(t, err); got != CodePreflightFailed {
		t.Fatalf("code = %q, want %q", got, CodePreflightFailed)
	}
	if git.called("push") != 0 || github.called("pr create") != 0 {
		t.Fatal("a refused publication still pushed or opened a pull request")
	}
	if !strings.Contains(err.Error(), ItemRevisionCurrent) {
		t.Fatalf("the message does not name the failing gate: %q", err.Error())
	}
}

func TestExecuteStagesWithControlPlaneExcluded(t *testing.T) {
	git := newFakeGit()
	publisher := newPublisher(t, git, newFakeGitHub())

	if _, err := publisher.Execute(t.Context(), newRequest(t), "Add the thing", "body"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var staged bool
	git.mutex.Lock()
	for _, args := range git.calls {
		operation, rest := verb(args)
		if operation == "add" && contains(rest, revision.ExcludeExchange) && contains(rest, revision.ExcludeDSStore) {
			staged = true
		}
	}
	git.mutex.Unlock()
	if !staged {
		t.Fatal("the publication commit did not exclude the control plane")
	}
}

func TestExecutePushesAnExplicitRefspecToTheFrozenURL(t *testing.T) {
	git := newFakeGit()
	publisher := newPublisher(t, git, newFakeGitHub())

	if _, err := publisher.Execute(t.Context(), newRequest(t), "Add the thing", "body"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	git.mutex.Lock()
	defer git.mutex.Unlock()
	for _, args := range git.calls {
		operation, rest := verb(args)
		if operation != "push" {
			continue
		}
		if !contains(rest, remoteURL) {
			t.Fatalf("push did not target the frozen remote URL: %v", rest)
		}
		if !contains(rest, "refs/heads/"+runBranch+":refs/heads/"+runBranch) {
			t.Fatalf("push did not name an explicit refspec: %v", rest)
		}
		if !contains(rest, "--atomic") {
			t.Fatalf("push was not atomic: %v", rest)
		}
		return
	}
	t.Fatal("no push was issued")
}

func TestExecuteReportsAFailedPush(t *testing.T) {
	git := newFakeGit()
	git.failures["push"] = revision.Result{
		ExitCode: 1,
		Stderr:   []byte("remote: Permission denied for token ghp_SECRET"),
	}
	github := newFakeGitHub()
	publisher := newPublisher(t, git, github)

	_, err := publisher.Execute(t.Context(), newRequest(t), "Add the thing", "body")
	if got := codeOf(t, err); got != CodePublishFailed {
		t.Fatalf("code = %q, want %q", got, CodePublishFailed)
	}
	if strings.Contains(err.Error(), "ghp_SECRET") {
		t.Fatalf("the client-facing message leaked a token: %q", err.Error())
	}
	if github.called("pr create") != 0 {
		t.Fatal("a pull request was opened after a failed push")
	}
}

func TestExecuteRefusesANonGitHubRemote(t *testing.T) {
	request := newRequest(t)
	request.RemoteURL = "ssh://git@gitlab.com/owner/repo.git"
	publisher := newPublisher(t, newFakeGit(), newFakeGitHub())

	_, err := publisher.Execute(t.Context(), request, "Add the thing", "body")
	if got := codeOf(t, err); got != CodeNoGitHub {
		t.Fatalf("code = %q, want %q", got, CodeNoGitHub)
	}
}

func TestExecuteRequiresATitle(t *testing.T) {
	publisher := newPublisher(t, newFakeGit(), newFakeGitHub())
	if _, err := publisher.Execute(t.Context(), newRequest(t), "", "body"); codeOf(t, err) != CodeInvalidRequest {
		t.Fatal("an empty title was accepted")
	}
}

func TestDraftIsPassedThroughOnCreate(t *testing.T) {
	request := newRequest(t)
	request.Draft = true
	github := newFakeGitHub()
	publisher := newPublisher(t, newFakeGit(), github)

	if _, err := publisher.Execute(t.Context(), request, "Add the thing", "body"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	github.mutex.Lock()
	defer github.mutex.Unlock()
	for _, args := range github.calls {
		if len(args) >= 2 && args[0]+" "+args[1] == "pr create" {
			if !contains(args, "--draft") {
				t.Fatalf("pr create did not request a draft: %v", args)
			}
			return
		}
	}
	t.Fatal("no pull request was created")
}

// ---------------------------------------------------------------------------
// Remote parsing and environment
// ---------------------------------------------------------------------------

func TestParseGitHubOwnerRepo(t *testing.T) {
	accepted := map[string][2]string{
		"https://github.com/owner/repo.git":   {"owner", "repo"},
		"https://github.com/owner/repo":       {"owner", "repo"},
		"https://www.github.com/owner/repo/":  {"owner", "repo"},
		"git@github.com:owner/repo.git":       {"owner", "repo"},
		"ssh://git@github.com/owner/repo.git": {"owner", "repo"},
		"http://github.com/owner/repo.git":    {"owner", "repo"},
	}
	for remote, want := range accepted {
		owner, repository, ok := ParseGitHubOwnerRepo(remote)
		if !ok || owner != want[0] || repository != want[1] {
			t.Errorf("ParseGitHubOwnerRepo(%q) = (%q, %q, %v)", remote, owner, repository, ok)
		}
	}

	refused := []string{
		"", "https://gitlab.com/owner/repo.git", "git@gitlab.com:owner/repo.git",
		"https://github.com/owner", "ext::sh -c 'curl evil'",
	}
	for _, remote := range refused {
		if _, _, ok := ParseGitHubOwnerRepo(remote); ok {
			t.Errorf("ParseGitHubOwnerRepo(%q) accepted a non-GitHub remote", remote)
		}
	}
}

func TestResolveRemoteBaseShaRefusesUnsafeInput(t *testing.T) {
	publisher := newPublisher(t, newFakeGit(), newFakeGitHub())

	if _, err := publisher.ResolveRemoteBaseSha(t.Context(), remoteURL, "../escape"); err == nil {
		t.Fatal("an unsafe base branch was accepted")
	}
	if _, err := publisher.ResolveRemoteBaseSha(t.Context(), "file:///tmp/repo", baseBranch); err == nil {
		t.Fatal("an unsafe remote was accepted")
	}
	sha, err := publisher.ResolveRemoteBaseSha(t.Context(), remoteURL, baseBranch)
	if err != nil || sha != baseSha {
		t.Fatalf("ResolveRemoteBaseSha = (%q, %v), want %q", sha, err, baseSha)
	}
}

func TestGitHubEnvironmentIsAnAllowlist(t *testing.T) {
	original := lookupEnv
	t.Cleanup(func() { lookupEnv = original })
	lookupEnv = func(name string) (string, bool) {
		values := map[string]string{
			"PATH":       "/usr/bin",
			"GH_TOKEN":   "token-value",
			"AWS_SECRET": "super-secret",
			"GIT_DIR":    "/elsewhere/.git",
		}
		value, ok := values[name]
		return value, ok
	}

	joined := strings.Join(githubEnvironment(), "\n")
	if !strings.Contains(joined, "GH_TOKEN=token-value") {
		t.Fatal("gh cannot authenticate without its token")
	}
	for _, forbidden := range []string{"AWS_SECRET", "GIT_DIR"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("%s reached the gh environment", forbidden)
		}
	}
	if !strings.Contains(joined, "GH_PROMPT_DISABLED=1") {
		t.Fatal("gh was not run non-interactively")
	}
}

func TestErrorsWithholdCommandOutput(t *testing.T) {
	err := newError(CodePublishFailed, "the run branch could not be pushed").
		withDetail("remote: token ghp_SECRET rejected at /Users/private/repo")
	if strings.Contains(err.Error(), "ghp_SECRET") || strings.Contains(err.Error(), "/Users/private") {
		t.Fatalf("the client-facing message leaked detail: %q", err.Error())
	}
	if err.Detail() == "" {
		t.Fatal("Detail() is empty, so the diagnostic was lost rather than withheld")
	}
	if !errors.Is(err, ErrPublication) {
		t.Fatal("errors.Is(err, ErrPublication) = false")
	}
}

func TestDecodePullRequestAcceptsBothShapes(t *testing.T) {
	if found, ok := decodePullRequest([]byte(`{"number":5,"url":"u"}`)); !ok || found.Number != 5 {
		t.Fatal("a single object was not decoded")
	}
	if found, ok := decodePullRequest([]byte(`[{"number":6,"url":"u"}]`)); !ok || found.Number != 6 {
		t.Fatal("an array was not decoded")
	}
	for _, payload := range []string{"", "   ", "[]", "null", "not json"} {
		if _, ok := decodePullRequest([]byte(payload)); ok {
			t.Errorf("decodePullRequest(%q) reported a pull request", payload)
		}
	}
}

func TestExecGitHubRunnerReportsAMissingBinary(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no shell available")
	}
	runner := ExecGitHubRunner{Binary: "definitely-not-a-real-gh"}
	if _, err := runner.Run(t.Context(), []string{"pr", "list"}, t.TempDir()); err == nil {
		t.Fatal("a missing gh binary was not reported")
	}
}
