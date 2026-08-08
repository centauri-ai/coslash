package sessiondetail

import (
	"regexp"
	"strconv"
	"strings"
)

type shellRead struct {
	Path          string
	StartLine     int
	ExpectedLines int
	Partial       bool
}

type pendingRead struct {
	Reads   []shellRead
	Command string
}

var (
	sedReadPattern = regexp.MustCompile(`(?:^|[;&|]\s*)sed\s+-n\s+['"]?(\d+),(\d+)p['"]?\s+([^\s;&|]+)`)
	catReadPattern = regexp.MustCompile(`(?:^|[;&|]\s*)cat\s+([^\s;&|]+)`)
)

func parseShellReads(command string) []shellRead {
	reads := []shellRead{}
	for _, match := range sedReadPattern.FindAllStringSubmatch(command, -1) {
		start, _ := strconv.Atoi(match[1])
		end, _ := strconv.Atoi(match[2])
		if start < 1 || end < start {
			continue
		}
		reads = append(reads, shellRead{Path: trimShellToken(match[3]), StartLine: start, ExpectedLines: end - start + 1, Partial: start > 1})
	}
	for _, match := range catReadPattern.FindAllStringSubmatch(command, -1) {
		reads = append(reads, shellRead{Path: trimShellToken(match[1]), StartLine: 1})
	}
	return reads
}

func trimShellToken(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"'`)
}

func splitReadOutput(reads []shellRead, output string) ([]string, bool) {
	if len(reads) == 1 {
		if reads[0].ExpectedLines > 0 && countLines(strings.TrimSuffix(output, "\n")) != reads[0].ExpectedLines {
			return nil, false
		}
		return []string{output}, true
	}
	for _, read := range reads {
		if read.ExpectedLines == 0 {
			return nil, false
		}
	}
	trimmed := strings.TrimSuffix(output, "\n")
	lines := strings.Split(trimmed, "\n")
	expected := 0
	for _, read := range reads {
		expected += read.ExpectedLines
	}
	if len(lines) != expected {
		return nil, false
	}
	pieces := make([]string, 0, len(reads))
	offset := 0
	for _, read := range reads {
		pieces = append(pieces, strings.Join(lines[offset:offset+read.ExpectedLines], "\n"))
		offset += read.ExpectedLines
	}
	return pieces, true
}

func noteReadOutput(h *heavyDetail, callID string, pending pendingRead, output string) {
	if output == "" || len(pending.Reads) == 0 {
		return
	}
	pieces, exact := splitReadOutput(pending.Reads, output)
	var groupID *string
	if !exact {
		id := callID
		groupID = &id
		h.readGroups = append(h.readGroups, ContextReadGroup{ID: callID, Command: pending.Command, Output: output})
	}
	for index, read := range pending.Reads {
		content := ""
		captured := exact
		if exact {
			content = pieces[index]
		} else if len(pending.Reads) == 1 {
			content = output
			captured = true
		}
		var total *int
		partial := read.Partial || !exact
		if exact && !partial {
			value := countLines(content)
			total = &value
		}
		h.addContext(read.Path, ContextSegment{StartLine: read.StartLine, Content: content}, partial, total, captured, groupID)
	}
}
