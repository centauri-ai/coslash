package session

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

func LatestFileModificationTime(cwd string, fileEdits []FileEdit) *int64 {
	return LatestFileModificationTimeContext(context.Background(), cwd, fileEdits)
}

func LatestFileModificationTimeContext(ctx context.Context, cwd string, fileEdits []FileEdit) *int64 {
	var newest *int64
	for _, edit := range fileEdits {
		if ctx.Err() != nil {
			return nil
		}
		path := edit.Path
		if !filepath.IsAbs(path) {
			if cwd == "" {
				continue
			}
			path = filepath.Join(cwd, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		m := info.ModTime().UnixMilli()
		if newest == nil || m > *newest {
			newest = &m
		}
	}
	return newest
}

func FileModificationTime(filePath string) int64 {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixMilli()
}

func CanonicalRepositoryName(cwd string) (string, bool) {
	return CanonicalRepositoryNameContext(context.Background(), cwd)
}

func CanonicalRepositoryNameContext(ctx context.Context, cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	fallback := filepath.Base(filepath.Clean(cwd))
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return fallback, true
	}
	root := RepositoryRootContext(ctx, resolved)
	if root == "" {
		return fallback, true
	}
	if output, err := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin").Output(); err == nil {
		if remote := canonicalRemoteName(string(output)); remote != "" {
			return remote, false
		}
	}
	gitMarker := filepath.Join(root, ".git")
	info, err := os.Stat(gitMarker)
	if err == nil && info.IsDir() {
		return filepath.Base(root), true
	}
	contents, err := os.ReadFile(gitMarker)
	if err != nil {
		return filepath.Base(root), true
	}
	gitDirValue := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(contents)), "gitdir:"))
	if gitDirValue == "" || gitDirValue == strings.TrimSpace(string(contents)) {
		return filepath.Base(root), true
	}
	gitDir := gitDirValue
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	common, err := os.ReadFile(filepath.Join(filepath.Clean(gitDir), "commondir"))
	if err != nil {
		return filepath.Base(root), true
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) == ".git" {
		return filepath.Base(filepath.Dir(commonDir)), true
	}
	return filepath.Base(root), true
}

// CanonicalOriginURL returns host/owner/repo for an scp-style or URL remote.
func CanonicalOriginURL(remote string) string {
	return canonicalRemoteName(remote)
}

func canonicalRemoteName(remote string) string {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		host, path, ok := strings.Cut(remote, ":")
		if !ok || host == "" || path == "" || strings.ContainsAny(host, `/\\`) {
			return ""
		}
		remote = "ssh://" + host + "/" + strings.TrimPrefix(path, "/")
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	path := strings.Trim(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(path), ".git") {
		path = path[:len(path)-len(".git")]
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}
	name := strings.ToLower(parsed.Host) + "/" + path
	if len(name) > snapshotv1.MaxRepositoryBytes || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return ""
	}
	return name
}

// RepositoryRoot returns the enclosing Git worktree root when one exists.
func RepositoryRoot(cwd string) string {
	return RepositoryRootContext(context.Background(), cwd)
}

func RepositoryRootContext(ctx context.Context, cwd string) string {
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return ""
	}
	for current := filepath.Clean(cwd); ; current = filepath.Dir(current) {
		if ctx.Err() != nil {
			return ""
		}
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
	}
}

func CurrentBranch(cwd string) *string {
	return CurrentBranchContext(context.Background(), cwd)
}

func CurrentBranchContext(ctx context.Context, cwd string) *string {
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err != nil {
		return nil
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return nil
	}
	return &branch
}

func BranchDrift(cwd string, recordedBranch *string) *GitDrift {
	return BranchDriftContext(context.Background(), cwd, recordedBranch)
}

func BranchDriftContext(ctx context.Context, cwd string, recordedBranch *string) *GitDrift {
	if cwd == "" || recordedBranch == nil {
		return nil
	}
	branch := strings.TrimSpace(*recordedBranch)
	if branch == "" {
		return nil
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return nil
	}
	baseBranch, baseRef := findBaseBranchContext(ctx, cwd)
	if baseBranch == "" || branch == baseBranch || branch == "refs/heads/"+baseBranch ||
		branch == "refs/remotes/origin/"+baseBranch {
		return nil
	}
	branchRef := branch
	if !strings.HasPrefix(branchRef, "refs/") {
		branchRef = "refs/heads/" + branchRef
	}
	out, err := exec.CommandContext(ctx,
		"git", "-C", cwd, "rev-list", "--left-right", "--count", branchRef+"..."+baseRef, "--",
	).
		Output()
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return nil
	}
	ahead, err1 := strconv.Atoi(fields[0])
	behind, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return nil
	}
	return &GitDrift{BaseBranch: baseBranch, Ahead: ahead, Behind: behind}
}

func findBaseBranch(cwd string) (string, string) {
	return findBaseBranchContext(context.Background(), cwd)
}

func findBaseBranchContext(ctx context.Context, cwd string) (string, string) {
	if out, err := exec.CommandContext(ctx,
		"git", "-C", cwd, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD",
	).Output(); err == nil {
		ref := strings.TrimSpace(string(out))
		if ref != "" && gitRefExistsContext(ctx, cwd, ref) {
			return filepath.Base(ref), ref
		}
	}
	for _, candidate := range []struct {
		name string
		ref  string
	}{
		{name: "main", ref: "refs/heads/main"},
		{name: "master", ref: "refs/heads/master"},
		{name: "main", ref: "refs/remotes/origin/main"},
		{name: "master", ref: "refs/remotes/origin/master"},
	} {
		if gitRefExistsContext(ctx, cwd, candidate.ref) {
			return candidate.name, candidate.ref
		}
	}
	return "", ""
}

func gitRefExists(cwd, ref string) bool {
	return gitRefExistsContext(context.Background(), cwd, ref)
}

func gitRefExistsContext(ctx context.Context, cwd, ref string) bool {
	return exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--verify", "--quiet", ref).Run() == nil
}
