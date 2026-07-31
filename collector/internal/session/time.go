package session

import (
	"fmt"
	"time"
)

func RFC3339ToUnixEpoch(ts string) (int64, error) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0, fmt.Errorf("parsing timestamp %q: %w", ts, err)
	}
	return t.UnixMilli(), nil
}

type TimestampRange struct {
	Earliest int64
	Latest   int64
}

func (timestamps *TimestampRange) Note(unixMilli int64) {
	timestamps.Latest = max(timestamps.Latest, unixMilli)
	if timestamps.Earliest == 0 || unixMilli < timestamps.Earliest {
		timestamps.Earliest = unixMilli
	}
}

func (timestamps TimestampRange) SpanMs() int {
	return int(timestamps.Latest - timestamps.Earliest)
}
