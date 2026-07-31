package session

import (
	"regexp"
	"strings"
)

// git commit as a complete command word, excludes plumbing commands (i.e.  commit-tree)
var gitCommitCommand = regexp.MustCompile(`(?:^|[\n;&|])\s*git\s+commit(?:$|[\s;&|])`)

// single or double-quoted
var commitMessage = regexp.MustCompile(`-m\s+(?:"([^"]+)"|'([^']+)')`)

// multilineBlockOpener matches the start of a shell here-document (<<EOF, <<'EOF', <<"EOF", <<-EOF)
var multilineBlockOpener = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?`)

func CommitMessage(command string) (string, bool) {
	matchIndices := gitCommitCommand.FindStringIndex(command)
	if matchIndices == nil {
		return "", false
	}
	// only match after git commit command, discard everything before
	matchStart := matchIndices[0]
	match := commitMessage.FindStringSubmatch(command[matchStart:])
	if match == nil {
		return "(commit)", true
	}
	message := match[1] // double quote message
	if message == "" {
		message = match[2] // single quote message
	}
	if body, ok := unwrapMultilineMessage(message); ok {
		return body, true
	}
	return message, true
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

var prCreateCommand = regexp.MustCompile(`(?:^|[\n;&|])\s*gh\s+pr\s+create\b`)
var prCreateHelpOrVersionCommand = regexp.MustCompile(
	`gh\s+pr\s+create\s+(?:-h|--help|--version)\b`,
)

func IsPullRequestCreate(command string) bool {
	return prCreateCommand.MatchString(command) &&
		!prCreateHelpOrVersionCommand.MatchString(command)
}
