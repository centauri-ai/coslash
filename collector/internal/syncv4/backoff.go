package syncv4

import "time"

// BaseCheckInCadence applies the Hub's advertised idle cadence. Active import
// work and explicit sync-now signals can wake earlier through the sync loop.
func BaseCheckInCadence(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 300
	}
	seconds = max(60, min(seconds, 900))
	return time.Duration(seconds) * time.Second
}

func retryBackoff(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return time.Minute
	case attempt == 2:
		return 5 * time.Minute
	case attempt == 3:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}

func retryDue(entry Entry, now time.Time) bool {
	return entry.RetryAt.IsZero() || !now.Before(entry.RetryAt)
}

func (q *Queue) NextRetryDelay() time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	var earliest time.Duration
	for _, entry := range q.state.Entries {
		if entry.RetryAt.IsZero() || entry.ParkedVersion != "" {
			continue
		}
		delay := entry.RetryAt.Sub(now)
		if delay <= 0 {
			return 0
		}
		if earliest == 0 || delay < earliest {
			earliest = delay
		}
	}
	return earliest
}
