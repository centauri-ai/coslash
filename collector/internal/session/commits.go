package session

import (
	"regexp"
	"strings"
)

// git commit as a complete command word, excludes plumbing commands (i.e.  commit-tree)
var gitCommitCommand = regexp.MustCompile(`(?:^|[\n;&|])\s*(?:rtk\s+)?git\s+commit(?:$|[\s;&|])`)

// -m, --message, or a combined short flag such as -am, single or double-quoted
var commitMessage = regexp.MustCompile(`(?:--message|-[a-zA-Z]*m)[=\s]*(?:"([^"]+)"|'([^']+)')`)

// -F or --file with its operand, which name the file the message comes from
var commitFileFlag = regexp.MustCompile(`(?:--file|-[a-zA-Z]*F)[=\s]+(\S+)`)

var commitAmend = regexp.MustCompile(`--amend\b`)

// multilineBlockOpener matches the start of a shell here-document (<<EOF, <<'EOF', <<"EOF", <<-EOF)
var multilineBlockOpener = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?`)

func CommitMessage(command string) (string, bool) {
	matchIndices := gitCommitCommand.FindStringIndex(command)
	if matchIndices == nil {
		return "", false
	}
	// only match after git commit command, discard everything before
	tail := command[matchIndices[0]:]
	if subject := commitSubject(tail); subject != "" {
		return subject, true
	}
	// the flags of this invocation alone, so that a later `git commit --amend` in
	// the same script cannot suppress the commit that this one creates. The match
	// ends on the separator or space after `commit`, so keep that byte.
	flags := command[matchIndices[1]-1:]
	if end := strings.IndexAny(flags, ";&|\n"); end >= 0 {
		flags = flags[:end]
	}
	if commitAmend.MatchString(flags) {
		return "", false
	}
	return "(commit)", true
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
	message := match[1] // double quote message
	if message == "" {
		message = match[2] // single quote message
	}
	if body, ok := unwrapMultilineMessage(message); ok {
		message = body
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
