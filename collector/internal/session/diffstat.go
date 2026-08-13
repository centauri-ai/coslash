package session

import "strings"

func DiffStat(lineChanges []string) (additions, deletions int) {
	for _, line := range lineChanges {
		switch {
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

func CountLines(content string) int {
	if content == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(content, "\n"), "\n"))
}
