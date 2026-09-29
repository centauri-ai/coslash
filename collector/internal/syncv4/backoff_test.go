package syncv4

import (
	"testing"
	"time"
)

func TestRetryBackoffAndParkedEntries(t *testing.T) {
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{{1, time.Minute}, {2, 5 * time.Minute}, {3, 15 * time.Minute}, {4, time.Hour}, {8, time.Hour}} {
		if got := retryBackoff(test.attempt); got != test.want {
			t.Fatalf("attempt %d: got %s, want %s", test.attempt, got, test.want)
		}
	}
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	entry := Entry{RetryAt: now.Add(time.Minute)}
	if retryDue(entry, now) || !retryDue(entry, now.Add(time.Minute)) {
		t.Fatal("retry window not enforced")
	}
	queue := &Queue{state: state{Entries: []Entry{{RetryAt: now.Add(time.Minute), ParkedVersion: "0.0.5"}}}}
	if delay := queue.NextRetryDelay(); delay != 0 {
		t.Fatalf("parked entry scheduled retry in %s", delay)
	}
}
