package session

const activityWindowMs = 2 * 60_000

func LiveStatus(inTurn bool, lastActivityAt, now int64) string {
	if inTurn {
		return "busy"
	}
	if lastActivityAt > 0 && now-lastActivityAt <= activityWindowMs {
		return "busy"
	}
	return "idle"
}
