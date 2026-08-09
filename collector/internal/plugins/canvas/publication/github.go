package publication

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GitHubResult is one `gh` invocation outcome.
type GitHubResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// GitHubRunner executes the GitHub CLI. Tests substitute a fake; no test in
// this suite may contact GitHub.
type GitHubRunner interface {
	Run(ctx context.Context, args []string, directory string) (GitHubResult, error)
}

// Bounds for a `gh` invocation.
const (
	githubTimeout        = 2 * time.Minute
	githubMaxOutputBytes = 4 << 20
)

// githubEnvironmentAllowlist names the process environment entries forwarded to
// `gh`. The token variables are included because `gh` authenticates with them;
// everything else the CLI might read is withheld.
var githubEnvironmentAllowlist = []string{
	"PATH",
	"HOME",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GH_CONFIG_DIR",
	"XDG_CONFIG_HOME",
	"SSH_AUTH_SOCK",
}

// ExecGitHubRunner runs the real `gh` binary with an explicit argv and no shell.
type ExecGitHubRunner struct {
	// Binary overrides the executable name. Tests point this at a fake gh.
	Binary string
}

func (r ExecGitHubRunner) binary() string {
	if r.Binary != "" {
		return r.Binary
	}
	return "gh"
}

// Run executes gh with bounded output and a hard timeout.
func (r ExecGitHubRunner) Run(ctx context.Context, args []string, directory string) (GitHubResult, error) {
	if len(args) == 0 {
		return GitHubResult{}, newError(CodeInvalidRequest, "the gh invocation has no arguments")
	}
	ctx, cancel := context.WithTimeout(ctx, githubTimeout)
	defer cancel()

	process := exec.CommandContext(ctx, r.binary(), args...)
	process.Dir = directory
	process.Env = githubEnvironment()
	process.Stdin = nil

	var stdout, stderr boundedBuffer
	stdout.limit = githubMaxOutputBytes
	stderr.limit = githubMaxOutputBytes
	process.Stdout = &stdout
	process.Stderr = &stderr

	runError := process.Run()
	result := GitHubResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if runError == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, newError(CodePublishFailed, "the GitHub CLI timed out").withCause(ctx.Err())
	}
	var exitError *exec.ExitError
	if errors.As(runError, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return result, newError(CodePublishFailed, "the GitHub CLI could not be started").
		withDetail(runError.Error()).withCause(runError)
}

func githubEnvironment() []string {
	environment := make([]string, 0, len(githubEnvironmentAllowlist)+3)
	for _, name := range githubEnvironmentAllowlist {
		if value, ok := lookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return append(environment,
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"NO_COLOR=1",
	)
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int64
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - int64(b.buffer.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(data), nil
	}
	if int64(len(data)) > remaining {
		b.buffer.Write(data[:remaining])
		b.truncated = true
		return len(data), nil
	}
	b.buffer.Write(data)
	return len(data), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }

// findPullRequest queries before creating. This query is the whole reason a
// retry cannot open a second pull request for one branch.
func (p *Publisher) findPullRequest(
	ctx context.Context,
	slug, branch, runRoot string,
) (*pullRequest, error) {
	result, err := p.github.Run(ctx, []string{
		"pr", "list", "-R", slug, "--head", branch, "--state", "all",
		"--json", "number,url", "--limit", "1",
	}, runRoot)
	if err != nil {
		return nil, newError(CodePublishFailed, "existing pull requests could not be listed").withCause(err)
	}
	if result.ExitCode != 0 {
		return nil, newError(CodePublishFailed, "existing pull requests could not be listed").
			withDetail(strings.TrimSpace(string(result.Stderr)))
	}
	found, ok := decodePullRequest(result.Stdout)
	if !ok {
		return nil, nil
	}
	return found, nil
}

func (p *Publisher) editPullRequest(
	ctx context.Context,
	slug string,
	number int,
	title, body, runRoot string,
) error {
	result, err := p.github.Run(ctx, []string{
		"pr", "edit", strconv.Itoa(number), "-R", slug,
		"--title", title, "--body", body,
	}, runRoot)
	if err != nil {
		return newError(CodePublishFailed, "the pull request could not be updated").withCause(err)
	}
	if result.ExitCode != 0 {
		return newError(CodePublishFailed, "the pull request could not be updated").
			withDetail(strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

type createOptions struct {
	Slug       string
	BaseBranch string
	Branch     string
	Title      string
	Body       string
	Draft      bool
	RunRoot    string
}

func (p *Publisher) createPullRequest(ctx context.Context, options createOptions) (pullRequest, error) {
	args := []string{
		"pr", "create", "-R", options.Slug,
		"--base", options.BaseBranch, "--head", options.Branch,
		"--title", options.Title, "--body", options.Body,
	}
	if options.Draft {
		args = append(args, "--draft")
	}
	result, err := p.github.Run(ctx, args, options.RunRoot)
	if err != nil {
		return pullRequest{}, newError(CodePublishFailed, "the pull request could not be created").withCause(err)
	}
	if result.ExitCode != 0 {
		return pullRequest{}, newError(CodePublishFailed, "the pull request could not be created").
			withDetail(strings.TrimSpace(string(result.Stderr)))
	}

	created := pullRequest{URL: strings.TrimSpace(string(result.Stdout))}

	// `gh pr create` prints a URL, not JSON. Read the number back so the record
	// carries a stable identifier; the URL alone is enough if this lookup fails.
	view, err := p.github.Run(ctx, []string{
		"pr", "view", options.Branch, "-R", options.Slug, "--json", "number,url",
	}, options.RunRoot)
	if err == nil && view.ExitCode == 0 {
		if found, ok := decodePullRequest(view.Stdout); ok {
			created.Number = found.Number
			if found.URL != "" {
				created.URL = found.URL
			}
		}
	}
	return created, nil
}
