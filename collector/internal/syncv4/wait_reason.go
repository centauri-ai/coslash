package syncv4

import (
	"context"
	"os"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type waitConditions struct {
	updateRequired bool
	pausedLocal    bool
	pausedHub      bool
	deviceOff      bool
	metered        bool
	battery        int
	conditionErr   bool
	spaceFull      bool
	backoff        bool
}

func waitReason(conditions waitConditions) string {
	switch {
	case conditions.updateRequired:
		return "update_required"
	case conditions.pausedLocal:
		return "paused_local"
	case conditions.deviceOff:
		return "device_off"
	case conditions.pausedHub:
		return "paused_hub"
	case conditions.metered:
		return "metered_network"
	case conditions.battery >= 0 && conditions.battery < 20:
		return "on_battery"
	case conditions.spaceFull:
		return "space_full"
	case conditions.conditionErr || conditions.backoff:
		return "backoff"
	default:
		return ""
	}
}

func (r *Runner) currentWait(ctx context.Context) *hubclient.V4ImportWait {
	conditions := waitConditions{
		updateRequired: r.Queue.UpdatePrompt().Required,
		pausedLocal:    os.Getenv("COSLASH_SYNC_PAUSED") == "1" || r.LocalPause != nil && r.LocalPause(),
		pausedHub:      r.config.Paused,
		deviceOff:      r.config.DeviceOff,
		battery:        -1,
	}
	if r.Conditions != nil {
		metered, battery, err := r.Conditions(ctx)
		conditions.metered, conditions.battery, conditions.conditionErr = metered, battery, err != nil
	}
	ready := false
	for _, entry := range r.Queue.Entries() {
		if entry.Excluded || entry.ParkedVersion != "" || !pending(entry) {
			continue
		}
		if !entry.RetryAt.IsZero() && r.now().Before(entry.RetryAt) {
			conditions.backoff = true
			conditions.spaceFull = conditions.spaceFull || entry.FailureCode == "space_full"
		} else {
			ready = true
		}
	}
	if ready {
		conditions.spaceFull, conditions.backoff = false, false
	}
	reason := waitReason(conditions)
	if reason == "" {
		r.waitReason, r.waitSince = "", time.Time{}
		return nil
	}
	if reason != r.waitReason {
		r.waitReason, r.waitSince = reason, r.now()
	}
	return &hubclient.V4ImportWait{Reason: reason, Since: r.waitSince.UTC()}
}
