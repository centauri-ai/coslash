package sessionexport

import (
	"encoding/json"
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
		reduceSessionMetadata,
	}
	for _, reduce := range reducers {
		if fits, err := reduce(&snapshot); err != nil {
			return snapshotv1.Snapshot{}, err
		} else if fits {
			return snapshot, nil
		}
	}

	size, err := encodedSnapshotSize(snapshot)
	if err != nil {
		return snapshotv1.Snapshot{}, err
	}
	return snapshotv1.Snapshot{}, fmt.Errorf(
		"mandatory snapshot facts are %d bytes after optional evidence reduction; maximum is %d: %w",
		size, snapshotv1.MaxPayloadBytes, snapshotv1.ErrOversized,
	)
}

func snapshotFits(snapshot *snapshotv1.Snapshot) (bool, error) {
	size, err := encodedSnapshotSize(*snapshot)
	if err != nil {
		return false, err
	}
	return size <= snapshotv1.MaxPayloadBytes, nil
}

// encodedSnapshotSize avoids repeating validation, hashing, and a second
// marshal during every aggregate-fitting probe. The placeholder has the same
// encoded length as the final SHA-256 content hash; snapshotv1.Marshal still
// performs the authoritative validation and hashing once after fitting.
func encodedSnapshotSize(snapshot snapshotv1.Snapshot) (int, error) {
	snapshot.ContentHash = "sha256:" + strings.Repeat("0", 64)
	data, err := json.Marshal(snapshot)
	if err != nil {
		return 0, fmt.Errorf("measure snapshot: %w", err)
	}
	return len(data), nil
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
	upsertAggregateBytes(snapshot, "/session/digest", originalBytes, remainingBytes)
	for i := range snapshot.Session.Digest {
		answer := snapshot.Session.Digest[i].Answer
		if answer == nil {
			continue
		}
		remainingBytes -= len(*answer)
		snapshot.Session.Digest[i].Answer = nil
		removeMetadata(snapshot, fmt.Sprintf("/session/digest/%d/answer", i))
		setAggregateBytes(snapshot, "/session/digest", remainingBytes)
		if fits, err := snapshotFits(snapshot); err != nil || fits {
			return fits, err
		}
	}
	return false, nil
}

func reduceOlderDigest(snapshot *snapshotv1.Snapshot) (bool, error) {
	return reduceNewest(snapshot, "/session/digest", &snapshot.Session.Digest)
}

func reduceTodos(snapshot *snapshotv1.Snapshot) (bool, error) {
	return reduceTail(snapshot, "/session/todos", &snapshot.Session.Todos)
}

func reduceCommits(snapshot *snapshotv1.Snapshot) (bool, error) {
	return reduceNewest(snapshot, "/session/commits", &snapshot.Session.Commits)
}

func reduceFileEdits(snapshot *snapshotv1.Snapshot) (bool, error) {
	return reduceNewest(snapshot, "/session/fileEdits", &snapshot.Session.FileEdits)
}

// reduceSessionMetadata removes optional envelope fields only after all
// collection evidence has been exhausted. The aggregate record targets the
// session object because removed optional properties no longer resolve as JSON
// Pointer targets.
func reduceSessionMetadata(snapshot *snapshotv1.Snapshot) (bool, error) {
	session := &snapshot.Session
	fields := []struct {
		path    string
		present bool
		clear   func()
	}{
		{"/session/firstPrompt", session.FirstPrompt != nil, func() { session.FirstPrompt = nil }},
		{"/session/declaredGoal", session.DeclaredGoal != nil, func() { session.DeclaredGoal = nil }},
		{"/session/summary", session.Summary != nil, func() { session.Summary = nil }},
		{"/session/name", session.Name != nil, func() { session.Name = nil }},
		{"/session/entrypoint", session.Entrypoint != nil, func() { session.Entrypoint = nil }},
		{"/session/branch", session.Branch != nil, func() { session.Branch = nil }},
		{"/session/cwd", session.WorkingDirectory != nil, func() { session.WorkingDirectory = nil }},
		{"/session/status", session.Status != nil, func() { session.Status = nil }},
		{"/session/git", session.Git != nil, func() { session.Git = nil }},
		{"/session/lastEditAtMs", session.LastEditAtMs != nil, func() { session.LastEditAtMs = nil }},
		{"/session/durationMs", session.DurationMs != nil, func() { session.DurationMs = nil }},
		{"/session/contextWindow", session.ContextWindow != nil, func() { session.ContextWindow = nil }},
		{"/session/contextTokens", session.ContextTokens != nil, func() { session.ContextTokens = nil }},
		{"/session/model", session.Model != nil, func() { session.Model = nil }},
	}

	original := 0
	for _, field := range fields {
		if field.present {
			original++
		}
	}
	if original == 0 {
		return false, nil
	}

	remaining := original
	upsertAggregateItems(snapshot, "/session", original, remaining)
	for _, field := range fields {
		if !field.present {
			continue
		}
		field.clear()
		removeMetadata(snapshot, field.path)
		remaining--
		setAggregateItems(snapshot, "/session", remaining)
		if fits, err := snapshotFits(snapshot); err != nil || fits {
			return fits, err
		}
	}
	return false, nil
}

func reduceTail[T any](snapshot *snapshotv1.Snapshot, path string, values *[]T) (bool, error) {
	if len(*values) == 0 {
		return false, nil
	}
	originalValues := *values
	original := len(originalValues)
	upsertAggregateItems(snapshot, path, original, original)
	metadata := captureMetadata(snapshot)
	*values = originalValues[:0]
	metadata.restore(snapshot)
	retainPrefixMetadata(snapshot, path, 0)
	setAggregateItems(snapshot, path, 0)
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
		setAggregateItems(snapshot, path, middle)
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
	setAggregateItems(snapshot, path, best)
	return true, nil
}

func reduceNewest[T any](snapshot *snapshotv1.Snapshot, path string, values *[]T) (bool, error) {
	if len(*values) == 0 {
		return false, nil
	}
	originalValues := *values
	original := len(originalValues)
	upsertAggregateItems(snapshot, path, original, original)
	metadata := captureMetadata(snapshot)
	*values = originalValues[original:]
	metadata.restore(snapshot)
	retainSuffixMetadata(snapshot, path, original)
	setAggregateItems(snapshot, path, 0)
	zeroFits, err := snapshotFits(snapshot)
	if err != nil || !zeroFits {
		return false, err
	}
	best := 0
	low, high := 1, original
	for low <= high {
		middle := low + (high-low)/2
		removed := original - middle
		*values = originalValues[removed:]
		metadata.restore(snapshot)
		retainSuffixMetadata(snapshot, path, removed)
		setAggregateItems(snapshot, path, middle)
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
	removed := original - best
	*values = originalValues[removed:]
	metadata.restore(snapshot)
	retainSuffixMetadata(snapshot, path, removed)
	setAggregateItems(snapshot, path, best)
	return true, nil
}

func reduceRequiredText(snapshot *snapshotv1.Snapshot, path string, value *string) (bool, error) {
	if value == nil || *value == "" {
		return false, nil
	}
	original := *value
	upsertAggregateBytes(snapshot, path, len(original), len(original))

	*value = ""
	setAggregateBytes(snapshot, path, 0)
	zeroFits, err := snapshotFits(snapshot)
	if err != nil || !zeroFits {
		return false, err
	}

	best := 0
	low, high := 1, utf8.RuneCountInString(original)
	for low <= high {
		middle := low + (high-low)/2
		length := byteIndexForRuneCount(original, middle)
		*value = original[:length]
		setAggregateBytes(snapshot, path, length)
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
	setAggregateBytes(snapshot, path, best)
	return true, nil
}

func byteIndexForRuneCount(value string, count int) int {
	seen := 0
	for index := range value {
		if seen == count {
			return index
		}
		seen++
	}
	return len(value)
}

func upsertAggregateBytes(snapshot *snapshotv1.Snapshot, path string, original, exported int) {
	for i := range snapshot.Truncation {
		item := &snapshot.Truncation[i]
		if item.Path == path && item.OriginalBytes != nil {
			item.Reason = snapshotv1.TruncationReasonAggregateBudget
			item.ExportedBytes = intPointer(exported)
			return
		}
	}
	snapshot.Truncation = append(snapshot.Truncation, snapshotv1.Truncation{
		Path: path, Reason: snapshotv1.TruncationReasonAggregateBudget,
		OriginalBytes: intPointer(original), ExportedBytes: intPointer(exported),
	})
}

func upsertAggregateItems(snapshot *snapshotv1.Snapshot, path string, original, exported int) {
	for i := range snapshot.Truncation {
		item := &snapshot.Truncation[i]
		if item.Path == path && item.OriginalItems != nil {
			item.Reason = snapshotv1.TruncationReasonAggregateBudget
			item.ExportedItems = intPointer(exported)
			return
		}
	}
	snapshot.Truncation = append(snapshot.Truncation, snapshotv1.Truncation{
		Path: path, Reason: snapshotv1.TruncationReasonAggregateBudget,
		OriginalItems: intPointer(original), ExportedItems: intPointer(exported),
	})
}

func setAggregateBytes(snapshot *snapshotv1.Snapshot, path string, exported int) {
	for i := range snapshot.Truncation {
		item := &snapshot.Truncation[i]
		if item.Path == path && item.Reason == snapshotv1.TruncationReasonAggregateBudget && item.ExportedBytes != nil {
			item.ExportedBytes = intPointer(exported)
			return
		}
	}
}

func setAggregateItems(snapshot *snapshotv1.Snapshot, path string, exported int) {
	for i := range snapshot.Truncation {
		item := &snapshot.Truncation[i]
		if item.Path == path && item.Reason == snapshotv1.TruncationReasonAggregateBudget && item.ExportedItems != nil {
			item.ExportedItems = intPointer(exported)
			return
		}
	}
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
		result[i].OriginalBytes = copyIntPointer(item.OriginalBytes)
		result[i].ExportedBytes = copyIntPointer(item.ExportedBytes)
		result[i].OriginalItems = copyIntPointer(item.OriginalItems)
		result[i].ExportedItems = copyIntPointer(item.ExportedItems)
	}
	return result
}

func cloneRedactions(values []snapshotv1.Redaction) []snapshotv1.Redaction {
	result := make([]snapshotv1.Redaction, len(values))
	copy(result, values)
	return result
}

func removeMetadata(snapshot *snapshotv1.Snapshot, removedPath string) {
	filterMetadata(snapshot, func(path string) (string, bool) {
		return path, path != removedPath && !strings.HasPrefix(path, removedPath+"/")
	})
}

func retainPrefixMetadata(snapshot *snapshotv1.Snapshot, collection string, retained int) {
	filterMetadata(snapshot, func(path string) (string, bool) {
		index, _, ok := indexedMetadataPath(path, collection)
		if ok && index >= retained {
			return "", false
		}
		return path, true
	})
}

func retainSuffixMetadata(snapshot *snapshotv1.Snapshot, collection string, removed int) {
	filterMetadata(snapshot, func(path string) (string, bool) {
		index, remainder, ok := indexedMetadataPath(path, collection)
		if !ok {
			return path, true
		}
		if index < removed {
			return "", false
		}
		return collection + "/" + strconv.Itoa(index-removed) + remainder, true
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

func filterMetadata(snapshot *snapshotv1.Snapshot, transform func(string) (string, bool)) {
	truncation := make([]snapshotv1.Truncation, 0, len(snapshot.Truncation))
	for _, item := range snapshot.Truncation {
		path, keep := transform(item.Path)
		if keep {
			item.Path = path
			truncation = append(truncation, item)
		}
	}
	snapshot.Truncation = truncation

	redactions := make([]snapshotv1.Redaction, 0, len(snapshot.Redactions))
	for _, item := range snapshot.Redactions {
		path, keep := transform(item.Path)
		if keep {
			item.Path = path
			redactions = append(redactions, item)
		}
	}
	snapshot.Redactions = redactions
}
