package syncv4

import (
	"context"
	"os"
	"strconv"
)

// LocalConditions reads available OS hints. The explicit metered override also
// lets a user pause sync on a connection whose OS does not expose a cost hint.
func LocalConditions(ctx context.Context) (bool, int, error) {
	if os.Getenv("COSLASH_SYNC_METERED") == "1" {
		return true, -1, nil
	}
	if os.Getenv("COSLASH_SYNC_OFFLINE") == "1" {
		return false, -1, ErrPaused
	}
	if value := os.Getenv("COSLASH_SYNC_BATTERY_PERCENT"); value != "" {
		percent, err := strconv.Atoi(value)
		if err != nil || percent < 0 || percent > 100 {
			return false, -1, ErrPaused
		}
		return false, percent, nil
	}
	probe, cancel := context.WithTimeout(ctx, conditionsTimeout)
	defer cancel()
	return platformConditions(probe)
}
