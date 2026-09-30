package session

// SelectedDigest preserves legacy transcripts and projects Pi's selected context.
func SelectedDigest(digest []DigestEntry) []DigestEntry {
	result := make([]DigestEntry, 0, len(digest))
	for _, entry := range digest {
		if entry.ContextSelected != nil && !*entry.ContextSelected {
			continue
		}
		if entry.ContextDescription != nil {
			entry.Description = *entry.ContextDescription
		}
		result = append(result, entry)
	}
	return result
}
