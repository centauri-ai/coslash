package remote

// staleSessions derives display provenance from normalized cached families.
// A partial refresh can therefore retain one failed family as stale without
// making unrelated, freshly replaced families look stale.
func staleSessions(snapshot CachedSnapshotV2) map[remoteSessionKey]bool {
	result := map[remoteSessionKey]bool{}
	for _, family := range snapshot.Families {
		if family.StaleReason == "" {
			continue
		}
		parsed, _, err := family.Facts.Parsed()
		if err != nil {
			continue
		}
		for _, item := range parsed {
			if item != nil && item.Session != nil {
				result[remoteSessionKey{Agent: item.Session.Agent, ID: item.Session.ID}] = true
			}
		}
	}
	return result
}
