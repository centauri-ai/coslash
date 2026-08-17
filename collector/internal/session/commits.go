package session

import (
	"os/exec"
	"regexp"
	"strings"
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

var commitHashToken = regexp.MustCompile(`(?:^|[^0-9a-fA-F])([0-9a-fA-F]{7,64})(?:$|[^0-9a-fA-F])`)

// multilineBlockOpener matches the start of a shell here-document (<<EOF, <<'EOF', <<"EOF", <<-EOF)
var multilineBlockOpener = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?`)

type CommitObservation struct {
	Hash    string
	Subject string
	Amend   bool
}

func ParseCommitObservations(command, output string, succeeded bool) []CommitObservation {
	invocations := commitInvocations(command)
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
	for _, match := range commitHashToken.FindAllStringSubmatch(output, -1) {
		hashes = append(hashes, match[1])
	}
	return hashes
}

func commitInvocations(command string) []CommitObservation {
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

func ReconcileCommits(observations []CommitObservation, cwd string, branch *string) []string {
	if len(observations) == 0 {
		return []string{}
	}
	history, ok := repositoryHistory(cwd, branch)
	if !ok {
		return fallbackCommitMessages(observations)
	}
	resolved := make([]int, len(observations))
	for i := range resolved {
		resolved[i] = -1
	}
	used := make([]bool, len(history))
	for i, observation := range observations {
		if observation.Hash == "" {
			continue
		}
		for j, commit := range history {
			if !used[j] && strings.HasPrefix(commit.hash, observation.Hash) &&
				(observation.Subject == "(commit)" || commit.subject == observation.Subject) {
				resolved[i], used[j] = j, true
				break
			}
		}
	}
	for i, observation := range observations {
		if resolved[i] >= 0 {
			continue
		}
		for j, commit := range history {
			if !used[j] && commit.subject == observation.Subject {
				resolved[i], used[j] = j, true
				break
			}
		}
	}
	messages := []string{}
	for _, index := range resolved {
		if index >= 0 {
			messages = append(messages, history[index].subject)
		}
	}
	return messages
}

func repositoryHistory(cwd string, branch *string) ([]repositoryCommit, bool) {
	if cwd == "" {
		return nil, false
	}
	ref := "HEAD"
	if branch != nil && strings.TrimSpace(*branch) != "" {
		ref = strings.TrimSpace(*branch)
	}
	output, err := exec.Command(
		"git", "-C", cwd, "log", "--format=%H%x00%s", ref, "--",
	).Output()
	if err != nil {
		if !gitRefExists(cwd, ref) &&
			exec.Command("git", "-C", cwd, "rev-parse", "--git-dir").Run() == nil {
			return []repositoryCommit{}, true
		}
		return nil, false
	}
	history := []repositoryCommit{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		hash, subject, found := strings.Cut(line, "\x00")
		if found && hash != "" {
			history = append(history, repositoryCommit{hash: hash, subject: subject})
		}
	}
	return history, true
}

func fallbackCommitMessages(observations []CommitObservation) []string {
	messages := []string{}
	for _, observation := range observations {
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

func IsPullRequestCreate(command string) bool {
	return prCreateCommand.MatchString(command) &&
		!prCreateHelpOrVersionCommand.MatchString(command)
}
