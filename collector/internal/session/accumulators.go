package session

import (
	"cmp"
	"slices"
	"strings"
)

// DigestLog and CommandLog are the accumulators both vendors' parsers keep, so
// the two parsers speak one vocabulary. Their zero values are ready to use and
// their getters never return nil — the JSON contract is "[] not null", and
// centralizing it here keeps each vendor from re-establishing it by hand.

type DigestLog struct {
	entries []DigestEntry
}

// Push appends an entry attributed to turn (at least 1). A recap keeps its
// full text; everything else is truncated for display.
func (log *DigestLog) Push(turn int, category, description string) {
	text := Truncate(description, TruncateTextLimit)
	if category == DigestRecap {
		text = strings.TrimSpace(description)
	}
	log.entries = append(log.entries, DigestEntry{
		Turn:        max(turn, 1),
		Category:    category,
		Description: text,
	})
}

func (log *DigestLog) PushQuestion(turn int, description, answer string) {
	log.Push(turn, DigestQuestion, description)
	log.entries[len(log.entries)-1].Answer = Truncate(answer, TruncateTextLimit)
}

func (log *DigestLog) PushSubagent(turn int, spawnKey string) {
	log.entries = append(log.entries, DigestEntry{
		Turn:     max(turn, 1),
		Category: DigestSubagent,
		SpawnKey: spawnKey,
	})
}

func (log *DigestLog) Entries() []DigestEntry {
	if log.entries == nil {
		return []DigestEntry{}
	}
	return log.entries
}

// LastRecap is the newest assistant recap, the session's working summary.
func (log *DigestLog) LastRecap() string {
	for _, entry := range slices.Backward(log.entries) {
		if entry.Category == DigestRecap {
			return entry.Description
		}
	}
	return ""
}

type CommandLog struct {
	entries []SubagentCommand
}

// Note records one command sighting. label is the vendor's human-readable
// description when it has one; the raw command stands in when it does not.
func (log *CommandLog) Note(command, label string) {
	log.entries = append(log.entries, SubagentCommand{
		Label:   cmp.Or(label, command),
		Command: command,
	})
}

func (log *CommandLog) Labelled() []SubagentCommand {
	if log.entries == nil {
		return []SubagentCommand{}
	}
	return log.entries
}

// Raw is the command strings alone, for SessionDetails.Commands.
func (log *CommandLog) Raw() []string {
	raw := make([]string, 0, len(log.entries))
	for _, entry := range log.entries {
		raw = append(raw, entry.Command)
	}
	return raw
}

func (log *CommandLog) Count() int {
	return len(log.entries)
}
