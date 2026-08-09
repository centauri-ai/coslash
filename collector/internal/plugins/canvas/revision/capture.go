package revision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MaxPatchBytes bounds a captured patch. A larger change is not something a
// reviewer can evaluate, and holding it in memory to hash is the reason the cap
// exists at all.
const MaxPatchBytes int64 = 8 << 20

// Pathspec exclusions applied to every capture.
//
// The literal forms `:(exclude).DS_Store` and `:(exclude).fleetlog` make
// `git add` exit non-zero when the path is gitignored and untracked — the
// common case for both. The glob forms exclude without that failure mode.
const (
	// ExcludeDSStore keeps Finder metadata out of revision identity. Without it,
	// Finder touching a directory changes a run's identity and invalidates an
	// approval nobody edited.
	ExcludeDSStore = ":(exclude,glob)**/.DS_Store"
	// ExcludeExchange keeps the control plane out of a published tree.
	ExcludeExchange = ":(exclude,glob)**/.fleetlog/**"
)

// FileStatus is git's single-letter change status.
type FileStatus string

const (
	StatusAdded    FileStatus = "A"
	StatusModified FileStatus = "M"
	StatusDeleted  FileStatus = "D"
	StatusRenamed  FileStatus = "R"
	StatusCopied   FileStatus = "C"
	StatusType     FileStatus = "T"
)

// ChangedFile is one path in a captured revision.
type ChangedFile struct {
	Path   string     `json:"path"`
	Status FileStatus `json:"status"`
}

// Revision is the immutable identity of one captured change.
type Revision struct {
	ChangeRevision uint64        `json:"changeRevision"`
	TreeOID        string        `json:"treeOid"`
	PatchSha256    string        `json:"patchSha256"`
	PatchBytes     int64         `json:"patchBytes"`
	ChangedFiles   []ChangedFile `json:"changedFiles"`
	Insertions     int64         `json:"insertions"`
	Deletions      int64         `json:"deletions"`
}

// CaptureOptions selects the run root and the scratch index used to snapshot it.
type CaptureOptions struct {
	RunRoot string
	// IndexFile is a scratch path OUTSIDE the run root. Pointing GIT_INDEX_FILE
	// here is what keeps $GIT_DIR/index byte-identical, so a concurrent agent's
	// staged/unstaged split survives the capture.
	IndexFile string
	// ExcludeExchangePaths omits the control plane from the snapshot. Review's
	// mutation guard sets this so a seat writing its own output under
	// `.fleetlog` does not count as a project-file mutation.
	ExcludeExchangePaths bool
}

// CaptureTreeOID snapshots a content-addressed tree without anchoring a
// revision or producing a patch.
//
// The temporary-index form is the only candidate that is complete,
// non-mutating, AND reproducible. The alternatives were measured and rejected:
//
//	git diff HEAD                     — misses untracked files
//	git add -A && git diff --cached   — irreversibly flattens the real index
//	git stash create                  — silently ignores -u, and embeds a timestamp
func (g *Git) CaptureTreeOID(ctx context.Context, options CaptureOptions) (string, error) {
	if err := prepareIndexFile(options); err != nil {
		return "", err
	}
	defer os.Remove(options.IndexFile)

	environment := []string{"GIT_INDEX_FILE=" + options.IndexFile}
	run := func(args ...string) (string, error) {
		return g.Output(ctx, Command{
			Args: append([]string{"-C", options.RunRoot}, args...),
			Env:  environment,
		})
	}

	if _, err := run("read-tree", "HEAD"); err != nil {
		return "", err
	}
	addArgs := []string{"add", "-A", "--", ExcludeDSStore}
	if options.ExcludeExchangePaths {
		addArgs = append(addArgs, ExcludeExchange)
	}
	if _, err := run(addArgs...); err != nil {
		return "", err
	}
	treeOID, err := run("write-tree")
	if err != nil {
		return "", err
	}
	if !ValidObjectID(treeOID) {
		return "", newError(CodeGitFailed, "git returned an unexpected tree object name")
	}
	return treeOID, nil
}

// CaptureRevisionOptions anchors a numbered revision for one run.
type CaptureRevisionOptions struct {
	CaptureOptions
	// RunID scopes the anchoring ref. It must be a safe ref component.
	RunID string
	// ChangeRevision is the monotonically increasing revision number.
	ChangeRevision uint64
	// BaseSha is the parent recorded on the anchoring commit.
	BaseSha string
	// RefNamespace is the ref prefix, for example "dagama" or "atlas".
	RefNamespace string
}

// CapturedRevision is a Revision plus the patch bytes it hashed.
type CapturedRevision struct {
	Revision
	Patch []byte
}

// CaptureRevision snapshots the run root, records the change, and anchors the
// resulting tree under refs so it cannot be garbage collected.
//
// write-tree leaves an UNREFERENCED object, which gc is free to delete.
// Anchoring it keeps an approved revision from disappearing out from under the
// gate that approved it.
func (g *Git) CaptureRevision(ctx context.Context, options CaptureRevisionOptions) (CapturedRevision, error) {
	if !ValidObjectID(options.BaseSha) {
		return CapturedRevision{}, newError(CodeBaseNotFound, "the base commit is not a full object name")
	}
	if !validRefComponent(options.RunID) || !validRefComponent(options.RefNamespace) {
		return CapturedRevision{}, newError(CodeInvalidPath, "the revision ref name is not valid")
	}
	if err := prepareIndexFile(options.CaptureOptions); err != nil {
		return CapturedRevision{}, err
	}
	defer os.Remove(options.IndexFile)

	environment := []string{"GIT_INDEX_FILE=" + options.IndexFile}
	run := func(args ...string) (string, error) {
		return g.Output(ctx, Command{
			Args: append([]string{"-C", options.RunRoot}, args...),
			Env:  environment,
		})
	}

	if _, err := run("read-tree", "HEAD"); err != nil {
		return CapturedRevision{}, err
	}
	addArgs := []string{"add", "-A", "--", ExcludeDSStore}
	if options.ExcludeExchangePaths {
		addArgs = append(addArgs, ExcludeExchange)
	}
	if _, err := run(addArgs...); err != nil {
		return CapturedRevision{}, err
	}
	treeOID, err := run("write-tree")
	if err != nil {
		return CapturedRevision{}, err
	}
	if !ValidObjectID(treeOID) {
		return CapturedRevision{}, newError(CodeGitFailed, "git returned an unexpected tree object name")
	}

	patch, err := g.RawOutput(ctx, Command{
		Args: []string{
			"-C", options.RunRoot,
			"-c", "core.abbrev=40",
			"diff", "--cached", "--no-color", "--no-ext-diff", "--no-textconv", "--binary", "HEAD",
		},
		Env:            environment,
		MaxOutputBytes: MaxPatchBytes + 1,
	})
	if err != nil {
		return CapturedRevision{}, err
	}
	if int64(len(patch)) > MaxPatchBytes {
		return CapturedRevision{}, newError(CodePatchTooLarge,
			fmt.Sprintf("the change is over the %d byte limit", MaxPatchBytes))
	}

	numstat, err := run("diff", "--cached", "--numstat", "--no-renames", "HEAD")
	if err != nil {
		return CapturedRevision{}, err
	}
	changedFiles, insertions, deletions := parseNumstat(numstat)

	nameStatus, err := run("diff", "--cached", "--name-status", "--no-renames", "HEAD")
	if err != nil {
		return CapturedRevision{}, err
	}
	changedFiles = applyNameStatus(changedFiles, nameStatus)

	commit, err := run("commit-tree", treeOID, "-p", options.BaseSha, "-m",
		fmt.Sprintf("%s revision %d", options.RefNamespace, options.ChangeRevision))
	if err != nil {
		return CapturedRevision{}, err
	}
	if !ValidObjectID(commit) {
		return CapturedRevision{}, newError(CodeGitFailed, "git returned an unexpected commit object name")
	}
	ref := fmt.Sprintf("refs/%s/runs/%s/rev/%d", options.RefNamespace, options.RunID, options.ChangeRevision)
	if _, err := run("update-ref", ref, commit); err != nil {
		return CapturedRevision{}, err
	}

	digest := sha256.Sum256(patch)
	return CapturedRevision{
		Revision: Revision{
			ChangeRevision: options.ChangeRevision,
			TreeOID:        treeOID,
			PatchSha256:    hex.EncodeToString(digest[:]),
			PatchBytes:     int64(len(patch)),
			ChangedFiles:   changedFiles,
			Insertions:     insertions,
			Deletions:      deletions,
		},
		Patch: patch,
	}, nil
}

// StatusPorcelain reports a run's working state for non-mutation assertions and
// for the UI.
func (g *Git) StatusPorcelain(ctx context.Context, runRoot string) (string, error) {
	return g.Output(ctx, Command{Args: []string{"-C", runRoot, "status", "--porcelain"}})
}

func prepareIndexFile(options CaptureOptions) error {
	if options.RunRoot == "" || !filepath.IsAbs(options.RunRoot) {
		return newError(CodeInvalidPath, "the run root must be an absolute path")
	}
	if options.IndexFile == "" || !filepath.IsAbs(options.IndexFile) {
		return newError(CodeInvalidPath, "the capture index must be an absolute path")
	}
	// The scratch index must live outside the run root; inside, an agent could
	// read or replace it mid-capture.
	runRoot := filepath.Clean(options.RunRoot) + string(filepath.Separator)
	if strings.HasPrefix(filepath.Clean(options.IndexFile), runRoot) {
		return newError(CodeInvalidPath, "the capture index must live outside the run root")
	}
	if err := os.MkdirAll(filepath.Dir(options.IndexFile), 0o700); err != nil {
		return newError(CodeInvalidPath, "the capture index directory could not be created").
			withDetail(err.Error()).withCause(err)
	}
	if err := os.Remove(options.IndexFile); err != nil && !os.IsNotExist(err) {
		return newError(CodeInvalidPath, "the stale capture index could not be removed").
			withDetail(err.Error()).withCause(err)
	}
	return nil
}

func parseNumstat(output string) ([]ChangedFile, int64, int64) {
	var files []ChangedFile
	var insertions, deletions int64
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		path := fields[2]
		if path == "" {
			continue
		}
		// '-' is git's marker for a binary file, not a zero.
		if fields[0] != "-" {
			if value, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
				insertions += value
			}
		}
		if fields[1] != "-" {
			if value, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				deletions += value
			}
		}
		files = append(files, ChangedFile{Path: path, Status: StatusModified})
	}
	return files, insertions, deletions
}

func applyNameStatus(files []ChangedFile, output string) []ChangedFile {
	statuses := make(map[string]FileStatus, len(files))
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		path := fields[len(fields)-1]
		if path == "" || fields[0] == "" {
			continue
		}
		statuses[path] = FileStatus(fields[0][:1])
	}
	for index := range files {
		if status, ok := statuses[files[index].Path]; ok {
			files[index].Status = status
		}
	}
	return files
}

// validRefComponent bounds a single path component embedded in a ref name.
func validRefComponent(component string) bool {
	if component == "" || len(component) > 128 {
		return false
	}
	if strings.Contains(component, "..") || strings.HasPrefix(component, "-") {
		return false
	}
	for _, character := range component {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_', character == '.':
		default:
			return false
		}
	}
	return true
}
