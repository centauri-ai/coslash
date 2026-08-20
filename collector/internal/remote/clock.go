package remote

import (
	"time"

	remoteviewv1 "github.com/centauri-ai/coslash/collector/remoteview/v1"
)

func clockOffsetMs(collectedAtMs int64, startedAt, finishedAt time.Time) int64 {
	mid := startedAt.UnixMilli() + (finishedAt.UnixMilli()-startedAt.UnixMilli())/2
	return collectedAtMs - mid
}

func adjustView(view remoteviewv1.View, offsetMs int64, nowMs int64) remoteviewv1.View {
	view.HostNowMs = clampFuture(view.HostNowMs-offsetMs, nowMs)
	view.CollectedAtMs = clampFuture(view.CollectedAtMs-offsetMs, nowMs)
	for i := range view.Sessions {
		view.Sessions[i] = adjustSession(view.Sessions[i], offsetMs, nowMs)
	}
	return view
}

func adjustSession(session remoteviewv1.Session, offsetMs, nowMs int64) remoteviewv1.Session {
	session.SessionStartedAtMs = clampFuture(session.SessionStartedAtMs-offsetMs, nowMs)
	session.LastActivityAtMs = clampFuture(session.LastActivityAtMs-offsetMs, nowMs)
	if session.LastEditAtMs != nil {
		adjusted := clampFuture(*session.LastEditAtMs-offsetMs, nowMs)
		session.LastEditAtMs = &adjusted
	}
	return session
}

func clampFuture(value, nowMs int64) int64 {
	if value > nowMs {
		return nowMs
	}
	if value < 0 {
		return 0
	}
	return value
}

func filterSessions(sessions []remoteviewv1.Session, sinceMs, nowMs int64) []remoteviewv1.Session {
	if sinceMs <= 0 {
		out := make([]remoteviewv1.Session, len(sessions))
		copy(out, sessions)
		return out
	}
	out := make([]remoteviewv1.Session, 0, len(sessions))
	for _, session := range sessions {
		if sessionVisible(session, sinceMs, nowMs) {
			out = append(out, session)
		}
	}
	return out
}

func sessionVisible(session remoteviewv1.Session, sinceMs, nowMs int64) bool {
	if isLiveSession(session, nowMs) {
		return true
	}
	return session.LastActivityAtMs >= sinceMs
}

func isLiveSession(session remoteviewv1.Session, nowMs int64) bool {
	if session.Status != nil {
		switch *session.Status {
		case "busy", "waiting":
			return true
		}
	}
	return session.LastActivityAtMs > 0 && nowMs-session.LastActivityAtMs <= ActivityWindowMs
}
