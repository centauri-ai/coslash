package sessionexport

import (
	"fmt"
	"strconv"
	"strings"
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
		if fits, err := reduceTail(snapshot, fmt.Sprintf("/session/subagents/%d/commandLabels", i), labels); err != nil || fits {
			return fits, err
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
		retargetMetadata(snapshot, fmt.Sprintf("/session/digest/%d/answer", i), fmt.Sprintf("/session/digest/%d", i))
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
	metadata := captureMetadata(snapshot)
	snapshot.Session.Digest = values[original:]
	metadata.restore(snapshot)
	retainSuffixMetadata(snapshot, "/session/digest", original)
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
		metadata.restore(snapshot)
		retainSuffixMetadata(snapshot, "/session/digest", original-middle)
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
	metadata.restore(snapshot)
	retainSuffixMetadata(snapshot, "/session/digest", original-best)
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
	metadata := captureMetadata(snapshot)
	*values = originalValues[:0]
	metadata.restore(snapshot)
	retainPrefixMetadata(snapshot, path, 0)
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
		metadata.restore(snapshot)
		retainPrefixMetadata(snapshot, path, middle)
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
	metadata.restore(snapshot)
	retainPrefixMetadata(snapshot, path, best)
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

type aggregateMetadata struct {
	truncation []snapshotv1.Truncation
	redactions []snapshotv1.Redaction
}

func captureMetadata(snapshot *snapshotv1.Snapshot) aggregateMetadata {
	return aggregateMetadata{
		truncation: cloneTruncation(snapshot.Truncation),
		redactions: cloneRedactions(snapshot.Redactions),
	}
}

func (metadata aggregateMetadata) restore(snapshot *snapshotv1.Snapshot) {
	snapshot.Truncation = cloneTruncation(metadata.truncation)
	snapshot.Redactions = cloneRedactions(metadata.redactions)
}

func cloneTruncation(values []snapshotv1.Truncation) []snapshotv1.Truncation {
	result := make([]snapshotv1.Truncation, len(values))
	for i, item := range values {
		result[i] = item
		result[i].OriginalBytes = copyOptionalInt(item.OriginalBytes)
		result[i].ExportedBytes = copyOptionalInt(item.ExportedBytes)
		result[i].OriginalItems = copyOptionalInt(item.OriginalItems)
		result[i].ExportedItems = copyOptionalInt(item.ExportedItems)
	}
	return result
}

func cloneRedactions(values []snapshotv1.Redaction) []snapshotv1.Redaction {
	result := make([]snapshotv1.Redaction, len(values))
	copy(result, values)
	return result
}

func copyOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func retargetMetadata(snapshot *snapshotv1.Snapshot, removedPath, target string) {
	transformMetadata(snapshot, func(path string) string {
		if path == removedPath || strings.HasPrefix(path, removedPath+"/") {
			return target
		}
		return path
	})
}

func retainPrefixMetadata(snapshot *snapshotv1.Snapshot, collection string, retained int) {
	transformMetadata(snapshot, func(path string) string {
		index, _, ok := indexedMetadataPath(path, collection)
		if ok && index >= retained {
			return collection
		}
		return path
	})
}

func retainSuffixMetadata(snapshot *snapshotv1.Snapshot, collection string, removed int) {
	transformMetadata(snapshot, func(path string) string {
		index, remainder, ok := indexedMetadataPath(path, collection)
		if !ok {
			return path
		}
		if index < removed {
			return collection
		}
		return collection + "/" + strconv.Itoa(index-removed) + remainder
	})
}

func indexedMetadataPath(path, collection string) (int, string, bool) {
	prefix := collection + "/"
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	segment, remainder, _ := strings.Cut(rest, "/")
	index, err := strconv.Atoi(segment)
	if err != nil || index < 0 {
		return 0, "", false
	}
	if remainder != "" {
		remainder = "/" + remainder
	}
	return index, remainder, true
}

func transformMetadata(snapshot *snapshotv1.Snapshot, transform func(string) string) {
	for i := range snapshot.Truncation {
		snapshot.Truncation[i].Path = transform(snapshot.Truncation[i].Path)
	}
	for i := range snapshot.Redactions {
		snapshot.Redactions[i].Path = transform(snapshot.Redactions[i].Path)
	}
}
