package session

import (
	"cmp"
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/backgroundexec"
)

// git commit at shell command position; excludes commit-tree and argument text.
var gitCommitCommand = regexp.MustCompile(
	`(?:^|[\n;&|])[ \t]*(?:rtk[ \t]+)?(git[ \t]+commit)(?:$|[ \t][^\n;&|]*)`,
)

// -m, --message, or a combined short flag such as -am, single or double-quoted
var commitMessage = regexp.MustCompile(`(?:--message|-[a-zA-Z]*m)[=\s]*(?:"([^"]+)"|'([^']+)')`)

// -F or --file with its operand, which name the file the message comes from
var commitFileFlag = regexp.MustCompile(`(?:--file|-[a-zA-Z]*F)[=\s]+(\S+)`)

var commitAmend = regexp.MustCompile(`--amend\b`)

var commitOutputHash = regexp.MustCompile(`(?m)\[[^\]\r\n]*[ \t]([0-9a-fA-F]{7,64})\]|^[ \t]*([0-9a-fA-F]{40}|[0-9a-fA-F]{64})[ \t]*\r?$`)

// multilineBlockOpener matches the start of a shell here-document (<<EOF, <<'EOF', <<"EOF", <<-EOF)
var multilineBlockOpener = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?`)

type CommitObservation struct {
	Hash    string
	Subject string
	Amend   bool
}

func ParseCommitObservations(command, output string, succeeded bool) []CommitObservation {
	invocations := ParseCommitAttempts(command)
	if len(invocations) == 0 {
		return invocations
	}
	hashes := commitOutputHashes(output)
	if len(invocations) == len(hashes) {
		for i := range invocations {
			invocations[i].Hash = hashes[i]
		}
		return invocations
	}
	if succeeded {
		return invocations
	}
	return []CommitObservation{}
}

func commitOutputHashes(output string) []string {
	hashes := []string{}
	for _, match := range commitOutputHash.FindAllStringSubmatch(output, -1) {
		hash := match[1]
		if hash == "" {
			hash = match[2]
		}
		hashes = append(hashes, hash)
	}
	return hashes
}

func ParseCommitAttempts(command string) []CommitObservation {
	matches := gitCommitCommand.FindAllStringSubmatchIndex(maskQuotedShellText(command), -1)
	observations := make([]CommitObservation, 0, len(matches))
	for _, match := range matches {
		invocation := command[match[2]:match[1]]
		subject := commitSubject(invocation)
		if subject == "" {
			subject = "(commit)"
		}
		observations = append(observations, CommitObservation{
			Subject: subject,
			Amend:   commitAmend.MatchString(invocation),
		})
	}
	return observations
}

func maskQuotedShellText(command string) string {
	masked := []byte(command)
	var quote byte
	for i := 0; i < len(masked); i++ {
		char := masked[i]
		if quote != '\'' && char == '\\' {
			masked[i] = ' '
			i++
			if i < len(masked) {
				masked[i] = ' '
			}
			continue
		}
		if quote != 0 {
			masked[i] = ' '
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			masked[i] = ' '
		}
	}
	return string(masked)
}

type repositoryCommit struct {
	hash    string
	subject string
}

// CommitFacts keeps human commit subjects and machine Git object IDs on
// separate paths. Hashes are emitted only when an observed value resolves
// against local repository history to a full object ID.
type CommitFacts struct {
	Subjects []string
	SHAs     []string
}

// NewCommitFactsReconciler caches repository history for one reconciliation
// pass while retaining resolved full object IDs for the public export mapper.
func NewCommitFactsReconciler() func([]CommitObservation, string, *string) CommitFacts {
	return NewCommitFactsReconcilerContext(context.Background())
}

func NewCommitFactsReconcilerContext(ctx context.Context) func([]CommitObservation, string, *string) CommitFacts {
	type cacheKey struct{ cwd, branch string }
	type cachedHistory struct {
		commits []repositoryCommit
		ok      bool
	}
	histories := map[cacheKey]cachedHistory{}
	return func(observations []CommitObservation, cwd string, branch *string) CommitFacts {
		if len(observations) == 0 {
			return CommitFacts{Subjects: []string{}, SHAs: []string{}}
		}
		branchName := ""
		if branch != nil {
			branchName = strings.TrimSpace(*branch)
		}
		key := cacheKey{cwd: cwd, branch: branchName}
		history, found := histories[key]
		if !found {
			history.commits, history.ok = repositoryHistoryContext(ctx, cwd, branch)
			histories[key] = history
		}
		return reconcileCommitFactsContext(ctx, observations, history.commits, history.ok)
	}
}

func ReconcileCommits(observations []CommitObservation, cwd string, branch *string) []string {
	return reconcileCommitFactsFromRepository(observations, cwd, branch).Subjects
}

func reconcileCommitFactsFromRepository(observations []CommitObservation, cwd string, branch *string) CommitFacts {
	if len(observations) == 0 {
		return CommitFacts{Subjects: []string{}, SHAs: []string{}}
	}
	history, ok := repositoryHistory(cwd, branch)
	return reconcileCommitFacts(observations, history, ok)
}

func reconcileCommitFacts(observations []CommitObservation, history []repositoryCommit, ok bool) CommitFacts {
	return reconcileCommitFactsContext(context.Background(), observations, history, ok)
}

func reconcileCommitFactsContext(ctx context.Context, observations []CommitObservation, history []repositoryCommit, ok bool) CommitFacts {
	if !ok {
		return CommitFacts{Subjects: fallbackCommitMessagesContext(ctx, observations), SHAs: []string{}}
	}
	resolved := make([]int, len(observations))
	hashResolved := make([]bool, len(observations))
	for i := range resolved {
		if ctx.Err() != nil {
			return CommitFacts{}
		}
		resolved[i] = -1
	}
	used := make([]bool, len(history))
	for i, observation := range observations {
		if ctx.Err() != nil {
			return CommitFacts{}
		}
		if observation.Hash == "" {
			continue
		}
		for j, commit := range history {
			if ctx.Err() != nil {
				return CommitFacts{}
			}
			if !used[j] && strings.HasPrefix(commit.hash, observation.Hash) &&
				(observation.Subject == "(commit)" || commit.subject == observation.Subject) {
				resolved[i], hashResolved[i], used[j] = j, true, true
				break
			}
		}
	}
	for i, observation := range observations {
		if ctx.Err() != nil {
			return CommitFacts{}
		}
		if resolved[i] >= 0 {
			continue
		}
		for j, commit := range history {
			if ctx.Err() != nil {
				return CommitFacts{}
			}
			if !used[j] && commit.subject == observation.Subject {
				resolved[i], used[j] = j, true
				break
			}
		}
	}
	messages, shas := []string{}, []string{}
	for i, index := range resolved {
		if ctx.Err() != nil {
			return CommitFacts{}
		}
		if index >= 0 {
			messages = append(messages, history[index].subject)
			if hashResolved[i] {
				// repositoryHistory uses %H, which is always a full object ID.
				shas = append(shas, history[index].hash)
			}
		}
	}
	return CommitFacts{Subjects: messages, SHAs: shas}
}

func repositoryHistory(cwd string, branch *string) ([]repositoryCommit, bool) {
	return repositoryHistoryContext(context.Background(), cwd, branch)
}

func repositoryHistoryContext(ctx context.Context, cwd string, branch *string) ([]repositoryCommit, bool) {
	if !backgroundFilesystemProbeAllowed(cwd) {
		return nil, false
	}
	ref := "HEAD"
	if branch != nil && strings.TrimSpace(*branch) != "" {
		ref = strings.TrimSpace(*branch)
	}
	output, err := backgroundexec.CommandContext(ctx,
		"git", "-C", cwd, "log", "--format=%H%x00%s", ref, "--",
	).Output()
	if err != nil {
		if !gitRefExistsContext(ctx, cwd, ref) &&
			backgroundexec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--git-dir").Run() == nil {
			return []repositoryCommit{}, true
		}
		return nil, false
	}
	history := []repositoryCommit{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if ctx.Err() != nil {
			return nil, false
		}
		hash, subject, found := strings.Cut(line, "\x00")
		if found && hash != "" {
			history = append(history, repositoryCommit{hash: hash, subject: subject})
		}
	}
	return history, true
}

func fallbackCommitMessages(observations []CommitObservation) []string {
	return fallbackCommitMessagesContext(context.Background(), observations)
}

func fallbackCommitMessagesContext(ctx context.Context, observations []CommitObservation) []string {
	messages := []string{}
	for _, observation := range observations {
		if ctx.Err() != nil {
			return nil
		}
		if observation.Amend && len(messages) > 0 {
			messages[len(messages)-1] = observation.Subject
			continue
		}
		messages = append(messages, observation.Subject)
	}
	return messages
}

func commitSubject(command string) string {
	flag := commitMessage.FindStringIndex(command)
	file := commitFileFlag.FindStringSubmatchIndex(command)
	if file != nil && (flag == nil || file[0] < flag[0]) {
		if command[file[2]:file[3]] != "-" {
			return "" // a named file, whose contents the transcript does not hold
		}
		body, _ := unwrapMultilineMessage(command) // `-F -` reads stdin
		return firstLine(body)
	}
	if flag == nil {
		return ""
	}
	match := commitMessage.FindStringSubmatch(command)
	doubleQuoted := match[1] != ""
	message := match[1] // double quote message
	if message == "" {
		message = match[2] // single quote message
	}
	if body, ok := unwrapMultilineMessage(message); ok {
		return firstLine(body)
	}
	if doubleQuoted && strings.ContainsAny(message, "$`") {
		return ""
	}
	return firstLine(message)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}

// unwrapMultilineMessage extracts  body of a `-m "$(cat <<'EOF' … EOF)"` commit message
func unwrapMultilineMessage(message string) (string, bool) {
	opener := multilineBlockOpener.FindStringSubmatchIndex(message)
	if opener == nil {
		return "", false
	}
	delimiter := message[opener[2]:opener[3]]
	afterOpener := message[opener[1]:]
	newline := strings.IndexByte(afterOpener, '\n')
	if newline < 0 {
		return "", false
	}
	lines := strings.Split(afterOpener[newline+1:], "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == delimiter { // terminator line ends the block
			return strings.TrimSpace(strings.Join(lines[:i], "\n")), true
		}
	}
	return "", false
}

var prCreateCommand = regexp.MustCompile(`(?:^|[\n;&|])\s*(?:rtk\s+)?gh\s+pr\s+create\b`)
var prCreateHelpOrVersionCommand = regexp.MustCompile(
	`gh\s+pr\s+create\s+(?:-h|--help|--version)\b`,
)
var pullRequestURLPattern = regexp.MustCompile(
	`https://github\.com/([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)/pull/([1-9][0-9]*)\b`,
)

// MaxPullRequestLinks bounds the pull request list of one session.
const MaxPullRequestLinks = 100

// PullRequestLink is one pull request that a successful agent action created.
type PullRequestLink struct {
	URL        string `json:"url"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
}

func IsPullRequestCreate(command string) bool {
	return prCreateCommand.MatchString(command) &&
		!prCreateHelpOrVersionCommand.MatchString(command)
}

// IsPullRequestCreateTool reports whether a tool name is the GitHub MCP create
// action. MCP clients prefix the name with the server, so only the suffix is stable.
func IsPullRequestCreateTool(name string) bool {
	return strings.HasSuffix(name, "create_pull_request")
}

// PullRequestURLs returns the GitHub pull request URLs in text. This is the
// only place that knows the accepted URL shape.
func PullRequestURLs(text string) []string {
	return pullRequestURLPattern.FindAllString(text, -1)
}

// CreatedPullRequestURLs returns the URLs that a create action produced.
// A GitHub MCP result can echo the pull request body, so only its first URL counts.
func CreatedPullRequestURLs(tool bool, output string) []string {
	urls := PullRequestURLs(output)
	if tool && len(urls) > 1 {
		urls = urls[:1]
	}
	return urls
}

// NewPullRequestLinks turns a URL set into the canonical list: sorted by
// repository, then number, and capped at MaxPullRequestLinks. It returns nil
// for an empty set.
func NewPullRequestLinks(urls map[string]struct{}) []PullRequestLink {
	var links []PullRequestLink
	for url := range urls {
		match := pullRequestURLPattern.FindStringSubmatch(url)
		if match == nil || match[0] != url {
			continue
		}
		number, _ := strconv.Atoi(match[3])
		links = append(links, PullRequestLink{URL: url, Repository: match[1] + "/" + match[2], Number: number})
	}
	slices.SortFunc(links, func(a, b PullRequestLink) int {
		return cmp.Or(cmp.Compare(a.Repository, b.Repository), cmp.Compare(a.Number, b.Number))
	})
	if len(links) > MaxPullRequestLinks {
		links = links[:MaxPullRequestLinks]
	}
	return links
}

// MergePullRequestLinks returns the union of two lists in canonical order.
func MergePullRequestLinks(a, b []PullRequestLink) []PullRequestLink {
	urls := make(map[string]struct{}, len(a)+len(b))
	for _, link := range a {
		urls[link.URL] = struct{}{}
	}
	for _, link := range b {
		urls[link.URL] = struct{}{}
	}
	return NewPullRequestLinks(urls)
}

// SetPullRequestLinks stores the links and keeps the count equal to their number.
func (d *SessionDetails) SetPullRequestLinks(links []PullRequestLink) {
	d.PullRequestLinks = links
	d.PullRequests = len(links)
}
