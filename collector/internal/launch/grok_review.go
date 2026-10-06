package launch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func sandboxGrokReview(spec reviewCommandSpec, workingDirectory, scratch string) (reviewCommandSpec, error) {
	if runtime.GOOS != "darwin" {
		if err := writeGrokReviewSandbox(filepath.Join(scratch, "home"), scratch); err != nil {
			return reviewCommandSpec{}, err
		}
		return spec, nil
	}
	executable, err := exec.LookPath(spec.bin)
	if err != nil {
		return reviewCommandSpec{}, fmt.Errorf("find Grok review executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return reviewCommandSpec{}, err
	}
	roots := map[string]string{"WORKTREE": workingDirectory, "SCRATCH": scratch, "EXECUTABLE": executable}
	for key, path := range roots {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return reviewCommandSpec{}, fmt.Errorf("resolve review sandbox %s: %w", key, err)
		}
		roots[key], err = filepath.Abs(resolved)
		if err != nil {
			return reviewCommandSpec{}, err
		}
	}
	auth, err := filepath.EvalSymlinks(filepath.Join(scratch, "home", "auth.json"))
	if os.IsNotExist(err) {
		auth = "/dev/null"
	} else if err != nil {
		return reviewCommandSpec{}, err
	}
	profile := filepath.Join(scratch, "review.sb")
	if err := os.WriteFile(profile, []byte(grokReviewSeatbelt), 0600); err != nil {
		return reviewCommandSpec{}, err
	}
	args := []string{"-f", profile}
	for _, key := range []string{"WORKTREE", "SCRATCH", "EXECUTABLE"} {
		args = append(args, "-D", key+"="+roots[key])
	}
	args = append(args, "-D", "AUTH="+auth, roots["EXECUTABLE"])
	for i := range spec.args {
		if spec.args[i] == "coslash-review" {
			spec.args[i] = "off"
		}
	}
	spec.bin = "/usr/bin/sandbox-exec"
	spec.args = append(args, spec.args...)
	spec.dir = roots["SCRATCH"]
	spec.env = append(spec.env, "TMPDIR="+roots["SCRATCH"])
	return spec, nil
}

// The kernel confines the entire CLI and its descendants. IP traffic permits
// provider requests. Only the system DNS socket is allowed, not runtime sockets.
const grokReviewSeatbelt = `(version 1)
(allow default)
(deny file-read-data)
(allow file-read-data
 (literal "/")
 (subpath "/System/Library")
 (subpath "/usr/lib") (subpath "/usr/bin") (subpath "/usr/sbin")
 (subpath "/usr/libexec") (subpath "/usr/share") (subpath "/bin") (subpath "/sbin")
 (subpath "/Library/Apple") (subpath "/private/etc/ssl")
 (literal "/dev/null") (literal "/dev/urandom") (literal "/dev/random")
 (subpath (param "WORKTREE")) (subpath (param "SCRATCH"))
 (literal (param "EXECUTABLE")) (literal (param "AUTH")))
(deny file-write*)
(allow file-write* (subpath (param "SCRATCH")) (literal "/dev/null"))
(deny network*)
(allow network-outbound (remote ip))
(allow network-outbound (remote unix-socket (literal "/private/var/run/mDNSResponder")))
`
