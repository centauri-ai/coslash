package syncv4

import "time"

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
