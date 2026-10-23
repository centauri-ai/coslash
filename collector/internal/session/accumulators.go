package session

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
)

// DigestLog and CommandLog are the accumulators vendor parsers keep, so they
// speak one vocabulary. Their zero values are ready to use and
// their getters never return nil — the JSON contract is "[] not null", and
// centralizing it here keeps each vendor from re-establishing it by hand.

type DigestLog struct {
	entries []DigestEntry
}

// Push appends an entry attributed to turn (at least 1). Recaps and plans keep
// their text up to the portable record limit; everything else is truncated for
// display. time is the epoch milliseconds when the event occurred (0 if unknown).
func (log *DigestLog) Push(turn int, category, description string, time int64) {
	text := Truncate(description, TruncateTextLimit)
	if category == DigestRecap || category == DigestPlan {
		text = strings.TrimSpace(description)
		if len(text) > fullsessionv1.MaxStringBytes {
			text = text[:fullsessionv1.MaxStringBytes]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
			text = strings.Clone(text)
		}
	}
	log.entries = append(log.entries, DigestEntry{
		Turn:        max(turn, 1),
		Category:    category,
		Description: text,
		Time:        time,
	})
}

func (log *DigestLog) PushQuestion(turn int, description, answer string, time int64) {
	log.Push(turn, DigestQuestion, description, time)
	log.entries[len(log.entries)-1].Answer = Truncate(answer, TruncateTextLimit)
}

func (log *DigestLog) PushSubagent(turn int, spawnKey string, time int64) {
	log.entries = append(log.entries, DigestEntry{
		Turn:     max(turn, 1),
		Category: DigestSubagent,
		SpawnKey: spawnKey,
		Time:     time,
	})
}

func (log *DigestLog) PushSubagentTask(turn int, spawnKey, task string, time int64) {
	log.PushSubagent(turn, spawnKey, time)
	log.entries[len(log.entries)-1].Description = Truncate(task, TruncateTextLimit)
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
	calls   map[string]int
	tracked bool
}

// Note records one command sighting. label is the vendor's human-readable
// description when it has one; the raw command stands in when it does not.
func (log *CommandLog) Note(command, label string) {
	log.entries = append(log.entries, SubagentCommand{
		Label:   cmp.Or(label, command),
		Command: command,
	})
}

// NoteCall is Note for a command with a tool call ID, so AttachOutput can pair
// the result with it later.
func (log *CommandLog) NoteCall(id, command, label string) {
	log.Note(command, label)
	log.tracked = true
	if id == "" {
		return
	}
	if log.calls == nil {
		log.calls = map[string]int{}
	}
	log.calls[id] = len(log.entries) - 1
}

// Pending reports whether the call noted under id still waits for its output.
func (log *CommandLog) Pending(id string) bool {
	_, ok := log.calls[id]
	return ok
}

// AttachOutput stores the output of the call noted under id. The caller has
// already cut it with TruncateOutput or TruncateOutputReader. An unknown ID is
// ignored. An empty output is a real output.
func (log *CommandLog) AttachOutput(id, output string) {
	index, ok := log.calls[id]
	if !ok {
		return
	}
	log.entries[index].Output = &output
	delete(log.calls, id)
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

// Outputs is the command output for each Raw entry, nil for a command without a
// result. It is nil when no command was noted by call ID, which means the
// vendor does not collect outputs.
func (log *CommandLog) Outputs() []*string {
	if !log.tracked || len(log.entries) == 0 {
		return nil
	}
	outputs := make([]*string, len(log.entries))
	for i, entry := range log.entries {
		outputs[i] = entry.Output
	}
	return outputs
}
