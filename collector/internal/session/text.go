package session

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// harness injected prompt patterns
var harnessWrapper = regexp.MustCompile(
	`^<(?:local-command-(?:stdout|caveat)` +
		`|command-(?:name|message)` +
		`|system-reminder` +
		`|bash-(?:input|stdout)` +
		`|task-notification)>`,
)

func IsHarnessWrapped(text string) bool {
	return harnessWrapper.MatchString(text)
}

func Truncate(text string, max int) string {
	splitText := strings.Fields(text)
	collapsedText := strings.Join(splitText, " ")
	runes := []rune(collapsedText)
	if len(runes) <= max {
		return collapsedText
	}
	return string(runes[:max-1]) + "…"
}

// CommandOutputLimit is the longest command output kept whole. A longer output
// keeps its first and last CommandOutputLimit/2 bytes.
const CommandOutputLimit = 16 << 10

const commandOutputHalf = CommandOutputLimit / 2

// outputEdge is how many bytes of each end cutOutput needs to find rune boundaries.
const outputEdge = commandOutputHalf + utf8.UTFMax

// TruncateOutput cuts a command output longer than CommandOutputLimit to its
// head and tail on UTF-8 rune boundaries, joined by a marker with the number of
// bytes removed. Whitespace is kept as is.
func TruncateOutput(output string) string {
	output = strings.ToValidUTF8(output, "\uFFFD")
	if len(output) <= CommandOutputLimit {
		return output
	}
	return cutOutput(output[:outputEdge], output[len(output)-outputEdge:], len(output))
}

// TruncateOutputReader is TruncateOutput for a stream. It holds at most
// CommandOutputLimit plus a few bytes, however long the stream is.
func TruncateOutputReader(reader io.Reader) (string, error) {
	first := make([]byte, CommandOutputLimit+1)
	n, err := io.ReadFull(reader, first)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return TruncateOutput(string(first[:n])), nil
	}
	if err != nil {
		return "", err
	}
	total := n
	tail := make([]byte, 0, 2*outputEdge)
	tail = append(tail, first[n-outputEdge:]...)
	chunk := make([]byte, 32<<10)
	for {
		read, err := reader.Read(chunk)
		total += read
		tail = append(tail, chunk[:read]...)
		if len(tail) > outputEdge+len(chunk) {
			tail = append(tail[:0], tail[len(tail)-outputEdge:]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return cutOutput(string(first[:outputEdge]), string(tail[len(tail)-outputEdge:]), total), nil
}

// cutOutput joins the head and tail of an output of total bytes. head holds its
// first outputEdge bytes and tail its last outputEdge bytes.
func cutOutput(head, tail string, total int) string {
	headEnd := commandOutputHalf
	for !utf8.RuneStart(head[headEnd]) {
		headEnd--
	}
	tailStart := len(tail) - commandOutputHalf
	for !utf8.RuneStart(tail[tailStart]) {
		tailStart++
	}
	removed := total - headEnd - (len(tail) - tailStart)
	return strings.ToValidUTF8(head[:headEnd], "\uFFFD") +
		fmt.Sprintf("\n… [%d bytes truncated] …\n", removed) +
		strings.ToValidUTF8(tail[tailStart:], "\uFFFD")
}
