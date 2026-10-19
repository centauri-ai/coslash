package syncv4

import (
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const (
	commandJournalLimit     = 1000
	commandJournalRetention = 7 * 24 * time.Hour
)

func (q *Queue) pruneCommands(now time.Time) {
	cutoff := now.Add(-commandJournalRetention)
	kept := make([]commandRecord, 0, min(len(q.state.Commands), commandJournalLimit))
	open := 0
	for _, command := range q.state.Commands {
		if command.At.IsZero() {
			command.At = now
		}
		if command.Result.Result != "" {
			open++
		}
		if command.Result.Result != "" || !command.At.Before(cutoff) {
			kept = append(kept, command)
		}
	}
	// A command with an unacknowledged result must survive pruning even if
	// more than a thousand commands arrive before the Hub accepts results.
	drop := max(0, len(kept)-max(commandJournalLimit, open))
	if drop > 0 {
		retained := make([]commandRecord, 0, len(kept)-drop)
		for _, command := range kept {
			if command.Result.Result == "" && drop > 0 {
				drop--
				continue
			}
			retained = append(retained, command)
		}
		kept = retained
	}
	q.state.Commands = kept
}

func (q *Queue) SetCommandProgress(id string, progress hubclient.V4CommandProgress) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.state.Commands {
		item := &q.state.Commands[i]
		if item.ID != id {
			continue
		}
		prior := item.Result
		item.Result = hubclient.V4CommandResult{CommandID: id, Result: "in_progress", Progress: &progress}
		if err := q.save(); err != nil {
			item.Result = prior
			return err
		}
		return nil
	}
	return nil
}
