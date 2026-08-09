// Package publication owns idempotent, gated Git and GitHub publication.
//
// Every git and gh call is argv-only with no shell. The remote is the URL
// captured at run preflight — never a remote name resolved through config an
// agent inside the run root can rewrite.
//
// Nothing here performs a workflow stage transition. Preflight reports whether
// publication is allowed and Execute performs it; deciding when to publish
// belongs to a controller.
package publication

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/revision"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/verification"
)

// Checklist item identifiers. These are stable and surface directly in the UI.
const (
	ItemReviewApproved  = "review_approved"
	ItemVerificationOK  = "verification_ok"
	ItemRevisionCurrent = "revision_current"
	ItemBaseUnmoved     = "base_unmoved"
	ItemNoControlPlane  = "no_control_plane"
	ItemNonEmptyChange  = "non_empty_change"
	ItemRemoteSafe      = "remote_safe"
	ItemRunBranch       = "run_branch"
)

// refusedTreePrefixes never reach a published tree. `.fleetlog` is the control
// plane; a workflow file is code the target repository executes on push, which
// is not something an agent gets to introduce through a run.
var refusedTreePrefixes = []string{
	".fleetlog/",
	".github/workflows/",
}

var refusedTreePaths = map[string]bool{
	".fleetlog": true,
}

// ChecklistItem is one publication gate.
type ChecklistItem struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// ReviewFact is the product-owned review outcome, reduced to what the gate
// needs. The review artifact schema itself belongs to DaGama and Atlas.
type ReviewFact struct {
	Approved       bool
	ChangeRevision uint64
}

// PreflightResult reports whether a run may publish and why.
type PreflightResult struct {
	OK             bool            `json:"ok"`
	ChangeRevision uint64          `json:"changeRevision"`
	TreeOID        string          `json:"treeOid"`
	PatchSha256    string          `json:"patchSha256"`
	Branch         string          `json:"branch"`
	BaseBranch     string          `json:"baseBranch"`
	BaseSha        string          `json:"baseSha"`
	RemoteURL      string          `json:"remoteUrl"`
	Draft          bool            `json:"draft"`
	Checklist      []ChecklistItem `json:"checklist"`
}

// FailedItems names the gates that refused, for a safe client-facing message.
func (r PreflightResult) FailedItems() []string {
	var failed []string
	for _, item := range r.Checklist {
		if !item.OK {
			failed = append(failed, item.ID)
		}
	}
	return failed
}

// Request is everything publication needs about one run. It carries facts, not
// a run state machine, so this package stays free of workflow transitions.
type Request struct {
	RunID   string
	RunRoot string
	// Branch is the run branch that is pushed.
	Branch string
	// BaseBranch is the pull-request target and the branch checked for movement.
	BaseBranch string
	// BaseSha is the tip BaseBranch had when the run was authorized.
	BaseSha string
	// RemoteURL is the URL captured at preflight.
	RemoteURL string
	// Revision is the frozen change this publication attests.
	Revision revision.Revision
	// Review and Verification are the upstream gate outcomes.
	Review       ReviewFact
	Verification *verification.Document
	// Draft opens the pull request as a draft.
	Draft bool
	// CaptureIndexFile is the scratch index used to re-measure the worktree.
	CaptureIndexFile string
}

// Record is the durable publication result.
type Record struct {
	ChangeRevision uint64    `json:"changeRevision"`
	CommitSha      string    `json:"commitSha"`
	Branch         string    `json:"branch"`
	Remote         string    `json:"remote"`
	PRURL          string    `json:"prUrl"`
	PRNumber       int       `json:"prNumber"`
	Action         string    `json:"action"`
	IdempotencyKey string    `json:"idempotencyKey"`
	PublishedAt    time.Time `json:"publishedAt"`
}

// Publication actions.
const (
	ActionCreated = "created"
	ActionUpdated = "updated"
)

// Publisher performs preflight and publication for one suite.
type Publisher struct {
	git    *revision.Git
	github GitHubRunner
	now    func() time.Time
}

// NewPublisher binds a hardened git and a gh runner.
func NewPublisher(git *revision.Git, github GitHubRunner, now func() time.Time) (*Publisher, error) {
	if git == nil {
		return nil, newError(CodeRunNotReady, "publication requires a git runner")
	}
	if github == nil {
		github = ExecGitHubRunner{}
	}
	if now == nil {
		now = time.Now
	}
	return &Publisher{git: git, github: github, now: now}, nil
}

// IdempotencyKey is the stable identity of one publication effect. A retry with
// the same run and revision must never produce a second pull request.
func IdempotencyKey(runID string, changeRevision uint64) string {
	return runID + ":" + strconv.FormatUint(changeRevision, 10)
}

// Preflight evaluates every gate without modifying the repository.
func (p *Publisher) Preflight(ctx context.Context, request Request) (PreflightResult, error) {
	if err := validateRequest(request); err != nil {
		return PreflightResult{}, err
	}

	result := PreflightResult{
		ChangeRevision: request.Revision.ChangeRevision,
		TreeOID:        request.Revision.TreeOID,
		PatchSha256:    request.Revision.PatchSha256,
		Branch:         request.Branch,
		BaseBranch:     request.BaseBranch,
		BaseSha:        request.BaseSha,
		RemoteURL:      request.RemoteURL,
		Draft:          request.Draft,
	}
	add := func(item ChecklistItem) { result.Checklist = append(result.Checklist, item) }

	reviewOK := request.Review.Approved && request.Review.ChangeRevision == request.Revision.ChangeRevision
	add(ChecklistItem{
		ID:     ItemReviewApproved,
		Label:  fmt.Sprintf("review approved revision %d", request.Revision.ChangeRevision),
		OK:     reviewOK,
		Detail: describeReview(request.Review, request.Revision.ChangeRevision),
	})

	verifyOK := false
	verifyDetail := "verification document missing"
	if request.Verification != nil {
		document := request.Verification
		matches := document.ChangeRevision == request.Revision.ChangeRevision
		acceptable := document.Verdict == verification.VerdictPassed ||
			document.Verdict == verification.VerdictSkipped
		verifyOK = matches && acceptable
		verifyDetail = fmt.Sprintf("verification %s for revision %d",
			document.Verdict, document.ChangeRevision)
	}
	add(ChecklistItem{
		ID:     ItemVerificationOK,
		Label:  fmt.Sprintf("verification passed for revision %d", request.Revision.ChangeRevision),
		OK:     verifyOK,
		Detail: verifyDetail,
	})

	// Mutation detection: re-measure the worktree and compare it to the tree the
	// review approved. The control plane is excluded because review and
	// verification write their own evidence there, and those writes are not
	// project-file mutations.
	currentTree, treeErr := p.git.CaptureTreeOID(ctx, revision.CaptureOptions{
		RunRoot:              request.RunRoot,
		IndexFile:            request.CaptureIndexFile,
		ExcludeExchangePaths: true,
	})
	revisionCurrent := treeErr == nil && currentTree == request.Revision.TreeOID
	revisionDetail := fmt.Sprintf("tree %s", shortOID(request.Revision.TreeOID))
	if treeErr != nil {
		revisionDetail = "the worktree could not be measured"
	} else if !revisionCurrent {
		revisionDetail = fmt.Sprintf("worktree tree %s does not match approved %s",
			shortOID(currentTree), shortOID(request.Revision.TreeOID))
	}
	add(ChecklistItem{
		ID:     ItemRevisionCurrent,
		Label:  fmt.Sprintf("worktree matches approved revision %d", request.Revision.ChangeRevision),
		OK:     revisionCurrent,
		Detail: revisionDetail,
	})

	currentBase, baseErr := p.ResolveRemoteBaseSha(ctx, request.RemoteURL, request.BaseBranch)
	baseUnmoved := baseErr == nil && currentBase != "" && currentBase == request.BaseSha
	baseDetail := fmt.Sprintf("origin/%s at %s", request.BaseBranch, shortOID(request.BaseSha))
	if !baseUnmoved {
		baseDetail = fmt.Sprintf("origin/%s moved or unreadable (was %s, now %s)",
			request.BaseBranch, shortOID(request.BaseSha), shortOIDOrUnknown(currentBase))
	}
	add(ChecklistItem{
		ID:     ItemBaseUnmoved,
		Label:  fmt.Sprintf("target branch unmoved (origin/%s)", request.BaseBranch),
		OK:     baseUnmoved,
		Detail: baseDetail,
	})

	refused, listErr := p.refusedPathsInTree(ctx, request.RunRoot, request.Revision.TreeOID)
	noControlPlane := listErr == nil && len(refused) == 0
	controlDetail := "clean"
	if listErr != nil {
		controlDetail = "the revision tree could not be listed"
	} else if len(refused) > 0 {
		controlDetail = "refused paths: " + strings.Join(firstN(refused, 5), ", ")
	}
	add(ChecklistItem{
		ID:     ItemNoControlPlane,
		Label:  "no control-plane or workflow paths in the revision tree",
		OK:     noControlPlane,
		Detail: controlDetail,
	})

	nonEmpty := len(request.Revision.ChangedFiles) > 0
	add(ChecklistItem{
		ID:     ItemNonEmptyChange,
		Label:  "the revision changes at least one file",
		OK:     nonEmpty,
		Detail: fmt.Sprintf("%d changed files", len(request.Revision.ChangedFiles)),
	})

	remoteErr := revision.ValidateRemoteURL(request.RemoteURL)
	remoteSafe := remoteErr == nil
	remoteDetail := "remote accepted"
	if !remoteSafe {
		remoteDetail = "the remote URL is missing or uses an unsupported transport"
	}
	add(ChecklistItem{
		ID:     ItemRemoteSafe,
		Label:  "the push remote is a supported transport",
		OK:     remoteSafe,
		Detail: remoteDetail,
	})

	branchOK := revision.ValidBranchName(request.Branch)
	add(ChecklistItem{
		ID:     ItemRunBranch,
		Label:  "run branch " + request.Branch,
		OK:     branchOK,
		Detail: request.Branch,
	})

	result.OK = true
	for _, item := range result.Checklist {
		if !item.OK {
			result.OK = false
			break
		}
	}
	return result, nil
}

// Execute publishes an approved revision: commit, push, then query-then-create
// the pull request.
//
// Retry safety rests on three facts rather than on a lock. The commit is
// skipped when HEAD already carries the idempotency key, the push targets one
// explicit refspec, and the pull request is queried before it is created. A
// second Execute for the same key therefore updates at most one pull request
// and never opens another.
func (p *Publisher) Execute(ctx context.Context, request Request, title, body string) (Record, error) {
	preflight, err := p.Preflight(ctx, request)
	if err != nil {
		return Record{}, err
	}
	if !preflight.OK {
		return Record{}, newError(CodePreflightFailed,
			"publish preflight failed: "+strings.Join(preflight.FailedItems(), ", "))
	}
	if title == "" {
		return Record{}, newError(CodeInvalidRequest, "a pull request title is required")
	}

	key := IdempotencyKey(request.RunID, request.Revision.ChangeRevision)
	commitSha, err := p.commitOnce(ctx, request, key, title)
	if err != nil {
		return Record{}, err
	}

	// One explicit refspec, never a configured default: push.default=nothing is
	// already set, and naming both sides keeps a rewritten local ref from
	// updating an unrelated remote branch.
	push, err := p.git.Run(ctx, revision.Command{Args: []string{
		"-C", request.RunRoot, "push", "--atomic", "--", request.RemoteURL,
		fmt.Sprintf("refs/heads/%s:refs/heads/%s", request.Branch, request.Branch),
	}})
	if err != nil {
		return Record{}, newError(CodePublishFailed, "the run branch could not be pushed").withCause(err)
	}
	if push.ExitCode != 0 {
		return Record{}, newError(CodePublishFailed, "the run branch could not be pushed").
			withDetail(strings.TrimSpace(string(push.Stderr)))
	}

	owner, repository, ok := ParseGitHubOwnerRepo(request.RemoteURL)
	if !ok {
		return Record{}, newError(CodeNoGitHub,
			"publication requires a github.com remote URL to open a pull request")
	}
	slug := owner + "/" + repository

	record := Record{
		ChangeRevision: request.Revision.ChangeRevision,
		CommitSha:      commitSha,
		Branch:         request.Branch,
		Remote:         request.RemoteURL,
		IdempotencyKey: key,
		PublishedAt:    p.now().UTC(),
	}

	existing, err := p.findPullRequest(ctx, slug, request.Branch, request.RunRoot)
	if err != nil {
		return Record{}, err
	}
	if existing != nil {
		if err := p.editPullRequest(ctx, slug, existing.Number, title, body, request.RunRoot); err != nil {
			return Record{}, err
		}
		record.PRNumber = existing.Number
		record.PRURL = existing.URL
		record.Action = ActionUpdated
		return record, nil
	}

	created, err := p.createPullRequest(ctx, createOptions{
		Slug:       slug,
		BaseBranch: request.BaseBranch,
		Branch:     request.Branch,
		Title:      title,
		Body:       body,
		Draft:      request.Draft,
		RunRoot:    request.RunRoot,
	})
	if err != nil {
		return Record{}, err
	}
	record.PRNumber = created.Number
	record.PRURL = created.URL
	record.Action = ActionCreated
	return record, nil
}

// commitOnce stages project files and commits, unless HEAD already carries this
// idempotency key — the retry case.
func (p *Publisher) commitOnce(
	ctx context.Context,
	request Request,
	key, title string,
) (string, error) {
	head, ok := p.git.Try(ctx, revision.Command{
		Args: []string{"-C", request.RunRoot, "log", "-1", "--format=%H%n%B"},
	})
	if ok && strings.Contains(head, "Idempotency-Key: "+key) {
		lines := strings.SplitN(head, "\n", 2)
		if len(lines) > 0 && revision.ValidObjectID(strings.TrimSpace(lines[0])) {
			return strings.TrimSpace(lines[0]), nil
		}
	}

	// Stage project files only — never the control plane.
	add, err := p.git.Run(ctx, revision.Command{Args: []string{
		"-C", request.RunRoot, "add", "-A", "--",
		revision.ExcludeExchange, revision.ExcludeDSStore,
	}})
	if err != nil {
		return "", newError(CodePublishFailed, "the change could not be staged").withCause(err)
	}
	if add.ExitCode != 0 {
		return "", newError(CodePublishFailed, "the change could not be staged").
			withDetail(strings.TrimSpace(string(add.Stderr)))
	}

	staged, err := p.git.Run(ctx, revision.Command{
		Args: []string{"-C", request.RunRoot, "diff", "--cached", "--quiet"},
	})
	if err != nil {
		return "", newError(CodePublishFailed, "the staged change could not be inspected").withCause(err)
	}
	if staged.ExitCode == 0 {
		return "", newError(CodeEmptyChange, "the revision stages no changes to publish")
	}

	message := fmt.Sprintf("%s\n\nChange revision %d.\nIdempotency-Key: %s\n",
		title, request.Revision.ChangeRevision, key)
	commit, err := p.git.Run(ctx, revision.Command{
		Args: []string{"-C", request.RunRoot, "commit", "--quiet", "-m", message},
		Env: []string{
			"GIT_AUTHOR_NAME=coSlash Canvas",
			"GIT_AUTHOR_EMAIL=canvas@localhost",
			"GIT_COMMITTER_NAME=coSlash Canvas",
			"GIT_COMMITTER_EMAIL=canvas@localhost",
		},
	})
	if err != nil {
		return "", newError(CodePublishFailed, "the publication commit failed").withCause(err)
	}
	if commit.ExitCode != 0 {
		return "", newError(CodePublishFailed, "the publication commit failed").
			withDetail(strings.TrimSpace(string(commit.Stderr)))
	}

	commitSha, err := p.git.Output(ctx, revision.Command{
		Args: []string{"-C", request.RunRoot, "rev-parse", "HEAD"},
	})
	if err != nil || !revision.ValidObjectID(commitSha) {
		return "", newError(CodePublishFailed, "the publication commit could not be read")
	}
	return commitSha, nil
}

// ResolveRemoteBaseSha reads the fresh tip of the publish base on the remote —
// not the project's local branch tip, which can lag an unfetched origin and
// falsely pass the "unmoved" gate.
func (p *Publisher) ResolveRemoteBaseSha(ctx context.Context, remoteURL, baseBranch string) (string, error) {
	branch := strings.TrimSpace(baseBranch)
	if branch == "" || !revision.ValidBranchName(branch) {
		return "", newError(CodeInvalidRequest, "the base branch is not a valid branch name")
	}
	if err := revision.ValidateRemoteURL(remoteURL); err != nil {
		return "", newError(CodeUnsafeRemote, "the remote URL is missing or unsupported").withCause(err)
	}
	listed, ok := p.git.Try(ctx, revision.Command{
		Args: []string{"ls-remote", "--", remoteURL, "refs/heads/" + branch},
	})
	if !ok {
		return "", newError(CodePublishFailed, "the remote base branch could not be read")
	}
	for _, line := range strings.Split(listed, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		candidate := strings.ToLower(fields[0])
		if revision.ValidObjectID(candidate) {
			return candidate, nil
		}
	}
	return "", newError(CodePublishFailed, "the remote base branch has no matching ref")
}

func (p *Publisher) refusedPathsInTree(ctx context.Context, runRoot, treeOID string) ([]string, error) {
	if !revision.ValidObjectID(treeOID) {
		return nil, newError(CodeInvalidRequest, "the revision tree is not a full object name")
	}
	listed, err := p.git.Output(ctx, revision.Command{
		Args: []string{"-C", runRoot, "ls-tree", "-r", "--name-only", treeOID},
	})
	if err != nil {
		return nil, err
	}
	var refused []string
	for _, line := range strings.Split(listed, "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		if refusedTreePaths[entry] {
			refused = append(refused, entry)
			continue
		}
		for _, prefix := range refusedTreePrefixes {
			if strings.HasPrefix(entry, prefix) {
				refused = append(refused, entry)
				break
			}
		}
	}
	return refused, nil
}

var (
	sshRemotePattern   = regexp.MustCompile(`^(?:ssh://)?git@github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`)
	httpsRemotePattern = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$`)
)

// ParseGitHubOwnerRepo extracts owner and repository from a GitHub remote URL.
func ParseGitHubOwnerRepo(remoteURL string) (owner, repository string, ok bool) {
	trimmed := strings.TrimSpace(remoteURL)
	for _, pattern := range []*regexp.Regexp{sshRemotePattern, httpsRemotePattern} {
		if match := pattern.FindStringSubmatch(trimmed); match != nil {
			return match[1], match[2], true
		}
	}
	return "", "", false
}

func validateRequest(request Request) error {
	switch {
	case request.RunID == "":
		return newError(CodeInvalidRequest, "the run identifier is required")
	case request.RunRoot == "":
		return newError(CodeRunNotReady, "the run has no root")
	case request.CaptureIndexFile == "":
		return newError(CodeRunNotReady, "a capture index path is required")
	case !revision.ValidBranchName(request.Branch):
		return newError(CodeInvalidRequest, "the run branch is not a valid branch name")
	case !revision.ValidBranchName(request.BaseBranch):
		return newError(CodeInvalidRequest, "the base branch is not a valid branch name")
	case !revision.ValidObjectID(request.BaseSha):
		return newError(CodeInvalidRequest, "the base commit is not a full object name")
	case !revision.ValidObjectID(request.Revision.TreeOID):
		return newError(CodeInvalidRequest, "the revision tree is not a full object name")
	}
	return nil
}

func describeReview(review ReviewFact, changeRevision uint64) string {
	if !review.Approved {
		return "review has not approved this run"
	}
	if review.ChangeRevision != changeRevision {
		return fmt.Sprintf("review approved revision %d, not %d", review.ChangeRevision, changeRevision)
	}
	return fmt.Sprintf("review approved revision %d", changeRevision)
}

func shortOID(oid string) string {
	if len(oid) <= 12 {
		return oid
	}
	return oid[:12]
}

func shortOIDOrUnknown(oid string) string {
	if oid == "" {
		return "unknown"
	}
	return shortOID(oid)
}

func firstN(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

// pullRequest is the subset of `gh` output this package consumes.
type pullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

func decodePullRequest(payload []byte) (*pullRequest, bool) {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return nil, false
	}
	var single pullRequest
	if err := json.Unmarshal([]byte(trimmed), &single); err == nil && single.Number > 0 {
		return &single, true
	}
	var many []pullRequest
	if err := json.Unmarshal([]byte(trimmed), &many); err == nil {
		for _, candidate := range many {
			if candidate.Number > 0 {
				result := candidate
				return &result, true
			}
		}
	}
	return nil, false
}
