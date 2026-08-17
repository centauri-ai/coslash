package sessionexport

import (
	"fmt"
	"unicode/utf8"

	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

// fitAggregate preserves mandatory identity, repository, time, count, usage,
// cost, and redaction facts. When a bounded snapshot is still too large, it
// reduces optional evidence in the documented order and records every change.
func fitAggregate(snapshot snapshotv1.Snapshot) (snapshotv1.Snapshot, error) {
	fits, err := snapshotFits(&snapshot)
	if err != nil || fits {
		return snapshot, err
	}

	reducers := []func(*snapshotv1.Snapshot) (bool, error){
		reduceCommandLabels,
		reduceSubagentProse,
		reduceDigestAnswers,
		reduceOlderDigest,
		reduceTodos,
		reduceCommits,
		reduceFileEdits,
	}
	for _, reduce := range reducers {
		if fits, err := reduce(&snapshot); err != nil {
			return snapshotv1.Snapshot{}, err
		} else if fits {
			return snapshot, nil
		}
	}

	size, err := snapshotv1.Size(snapshot)
	if err != nil {
		return snapshotv1.Snapshot{}, err
	}
	return snapshotv1.Snapshot{}, fmt.Errorf(
		"mandatory snapshot facts are %d bytes after optional evidence reduction; maximum is %d: %w",
		size, snapshotv1.MaxPayloadBytes, snapshotv1.ErrOversized,
	)
}

func snapshotFits(snapshot *snapshotv1.Snapshot) (bool, error) {
	size, err := snapshotv1.Size(*snapshot)
	if err != nil {
		return false, err
	}
	return size <= snapshotv1.MaxPayloadBytes, nil
}

func reduceCommandLabels(snapshot *snapshotv1.Snapshot) (bool, error) {
	for i := len(snapshot.Session.Subagents) - 1; i >= 0; i-- {
		labels := &snapshot.Session.Subagents[i].CommandLabels
		if len(*labels) == 0 {
			continue
		}
		original := len(*labels)
		entry := appendAggregateItems(snapshot, fmt.Sprintf("/session/subagents/%d/commandLabels", i), original, original)
		for len(*labels) > 0 {
			*labels = (*labels)[:len(*labels)-1]
			*snapshot.Truncation[entry].ExportedItems = len(*labels)
			if fits, err := snapshotFits(snapshot); err != nil || fits {
				return fits, err
			}
		}
	}
	return false, nil
}

func reduceSubagentProse(snapshot *snapshotv1.Snapshot) (bool, error) {
	for i := len(snapshot.Session.Subagents) - 1; i >= 0; i-- {
		subagent := &snapshot.Session.Subagents[i]
		if fits, err := reduceRequiredText(snapshot, fmt.Sprintf("/session/subagents/%d/result", i), &subagent.Result); err != nil || fits {
			return fits, err
		}
		if fits, err := reduceRequiredText(snapshot, fmt.Sprintf("/session/subagents/%d/task", i), &subagent.Task); err != nil || fits {
			return fits, err
		}
	}
	return false, nil
}

func reduceDigestAnswers(snapshot *snapshotv1.Snapshot) (bool, error) {
	originalBytes := 0
	remainingBytes := 0
	for _, digest := range snapshot.Session.Digest {
		if digest.Answer != nil {
			originalBytes += len(*digest.Answer)
			remainingBytes += len(*digest.Answer)
		}
	}
	if originalBytes == 0 {
		return false, nil
	}
	entry := appendAggregateBytes(snapshot, "/session/digest", originalBytes, remainingBytes)
	for i := range snapshot.Session.Digest {
		answer := snapshot.Session.Digest[i].Answer
		if answer == nil {
			continue
		}
		remainingBytes -= len(*answer)
		snapshot.Session.Digest[i].Answer = nil
		*snapshot.Truncation[entry].ExportedBytes = remainingBytes
		if fits, err := snapshotFits(snapshot); err != nil || fits {
			return fits, err
		}
	}
	return false, nil
}

func reduceOlderDigest(snapshot *snapshotv1.Snapshot) (bool, error) {
	if len(snapshot.Session.Digest) == 0 {
		return false, nil
	}
	values := snapshot.Session.Digest
	original := len(values)
	entry := appendAggregateItems(snapshot, "/session/digest", original, original)
	snapshot.Session.Digest = values[original:]
	*snapshot.Truncation[entry].ExportedItems = 0
	zeroFits, err := snapshotFits(snapshot)
	if err != nil || !zeroFits {
		return false, err
	}
	best := 0
	low, high := 1, original
	for low <= high {
		middle := low + (high-low)/2
		snapshot.Session.Digest = values[original-middle:]
		*snapshot.Truncation[entry].ExportedItems = middle
		fits, err := snapshotFits(snapshot)
		if err != nil {
			return false, err
		}
		if fits {
			best = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	snapshot.Session.Digest = values[original-best:]
	*snapshot.Truncation[entry].ExportedItems = best
	return true, nil
}

func reduceTodos(snapshot *snapshotv1.Snapshot) (bool, error) {
	return reduceTail(snapshot, "/session/todos", &snapshot.Session.Todos)
}

func reduceCommits(snapshot *snapshotv1.Snapshot) (bool, error) {
	return reduceTail(snapshot, "/session/commits", &snapshot.Session.Commits)
}

func reduceFileEdits(snapshot *snapshotv1.Snapshot) (bool, error) {
	return reduceTail(snapshot, "/session/fileEdits", &snapshot.Session.FileEdits)
}

func reduceTail[T any](snapshot *snapshotv1.Snapshot, path string, values *[]T) (bool, error) {
	if len(*values) == 0 {
		return false, nil
	}
	originalValues := *values
	original := len(originalValues)
	entry := appendAggregateItems(snapshot, path, original, original)
	*values = originalValues[:0]
	*snapshot.Truncation[entry].ExportedItems = 0
	zeroFits, err := snapshotFits(snapshot)
	if err != nil || !zeroFits {
		return false, err
	}
	best := 0
	low, high := 1, original
	for low <= high {
		middle := low + (high-low)/2
		*values = originalValues[:middle]
		*snapshot.Truncation[entry].ExportedItems = middle
		fits, err := snapshotFits(snapshot)
		if err != nil {
			return false, err
		}
		if fits {
			best = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	*values = originalValues[:best]
	*snapshot.Truncation[entry].ExportedItems = best
	return true, nil
}

func reduceRequiredText(snapshot *snapshotv1.Snapshot, path string, value *string) (bool, error) {
	if value == nil || *value == "" {
		return false, nil
	}
	original := *value
	entry := appendAggregateBytes(snapshot, path, len(original), len(original))
	boundaries := runeBoundaries(original)

	*value = ""
	*snapshot.Truncation[entry].ExportedBytes = 0
	zeroFits, err := snapshotFits(snapshot)
	if err != nil || !zeroFits {
		return false, err
	}

	best := 0
	low, high := 1, len(boundaries)-1
	for low <= high {
		middle := low + (high-low)/2
		length := boundaries[middle]
		*value = original[:length]
		*snapshot.Truncation[entry].ExportedBytes = length
		fits, err := snapshotFits(snapshot)
		if err != nil {
			return false, err
		}
		if fits {
			best = length
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	*value = original[:best]
	*snapshot.Truncation[entry].ExportedBytes = best
	return true, nil
}

func runeBoundaries(value string) []int {
	boundaries := []int{0}
	for index := range value {
		if index > 0 && utf8.RuneStart(value[index]) {
			boundaries = append(boundaries, index)
		}
	}
	if boundaries[len(boundaries)-1] != len(value) {
		boundaries = append(boundaries, len(value))
	}
	return boundaries
}

func appendAggregateBytes(snapshot *snapshotv1.Snapshot, path string, original, exported int) int {
	snapshot.Truncation = append(snapshot.Truncation, snapshotv1.Truncation{
		Path: path, Reason: snapshotv1.TruncationReasonAggregateBudget,
		OriginalBytes: intPointer(original), ExportedBytes: intPointer(exported),
	})
	return len(snapshot.Truncation) - 1
}

func appendAggregateItems(snapshot *snapshotv1.Snapshot, path string, original, exported int) int {
	snapshot.Truncation = append(snapshot.Truncation, snapshotv1.Truncation{
		Path: path, Reason: snapshotv1.TruncationReasonAggregateBudget,
		OriginalItems: intPointer(original), ExportedItems: intPointer(exported),
	})
	return len(snapshot.Truncation) - 1
}
