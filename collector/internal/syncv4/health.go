package syncv4

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/settings"
)

type HealthSnapshot struct {
	InstallID         string
	PolicyKnown       bool
	HubPaused         bool
	HubDeviceOff      bool
	LastCheckInAt     *time.Time
	LastCheckInResult string
	LastHubFailure    *HubContactFailure
	Pending           int
	Failing           int
	Parked            int
	TopFailureCodes   []FailureCodeCount
}

type HubContactFailure struct {
	Cause    string `json:"cause"`
	SinceMs  int64  `json:"sinceMs"`
	Attempts int    `json:"attempts"`
	RawError string `json:"rawError"`
}

type FailureCodeCount struct {
	Code  string
	Count int
}

var healthFailureCodePattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// ReadHealth returns the content-free queue state used by coslash doctor. It
// reads the private queue file without opening, repairing, or rewriting it.
func ReadHealth(root string) (HealthSnapshot, error) {
	if root == "" {
		root = filepath.Join(settings.Home(), "sync-v4")
	}
	data, err := readQueueFile(filepath.Join(root, "queue.json"))
	if err != nil {
		return HealthSnapshot{}, err
	}
	var saved state
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version != 1 || saved.InstallID == "" {
		return HealthSnapshot{}, errors.New("invalid v4 queue")
	}
	health := HealthSnapshot{
		InstallID: saved.InstallID, PolicyKnown: saved.PolicyKnown,
		HubPaused: saved.Config.Paused, HubDeviceOff: saved.Config.DeviceOff,
		LastCheckInResult: saved.LastCheckInResult,
		TopFailureCodes:   []FailureCodeCount{},
	}
	if saved.LastHubFailure != nil && validHubContactFailure(*saved.LastHubFailure) {
		failure := *saved.LastHubFailure
		health.LastHubFailure = &failure
	}
	if health.LastCheckInResult != "" {
		health.LastCheckInResult = safeCheckInResult(health.LastCheckInResult)
	}
	if saved.LastCheckInAtMs > 0 {
		at := time.UnixMilli(saved.LastCheckInAtMs)
		health.LastCheckInAt = &at
	}
	failures := map[string]int{}
	for _, entry := range saved.Entries {
		if entry.Excluded {
			continue
		}
		if pending(entry) {
			health.Pending++
		}
		if entry.FailureCode != "" {
			health.Failing++
			code := entry.FailureCode
			if !healthFailureCodePattern.MatchString(code) {
				code = "unknown"
			}
			failures[code]++
		}
		if entry.ParkedVersion != "" {
			health.Parked++
		}
	}
	for code, count := range failures {
		health.TopFailureCodes = append(health.TopFailureCodes, FailureCodeCount{Code: code, Count: count})
	}
	sort.Slice(health.TopFailureCodes, func(i, j int) bool {
		if health.TopFailureCodes[i].Count != health.TopFailureCodes[j].Count {
			return health.TopFailureCodes[i].Count > health.TopFailureCodes[j].Count
		}
		return health.TopFailureCodes[i].Code < health.TopFailureCodes[j].Code
	})
	if len(health.TopFailureCodes) > 3 {
		health.TopFailureCodes = health.TopFailureCodes[:3]
	}
	return health, nil
}

func validHubContactFailure(failure HubContactFailure) bool {
	switch failure.Cause {
	case hubclient.FailureDNS, hubclient.FailureConnect, hubclient.FailureTLS, hubclient.FailureProxy,
		hubclient.FailureHubUnavailable, hubclient.FailureRejected:
	default:
		return false
	}
	return failure.SinceMs > 0 && failure.Attempts > 0 && len(failure.RawError) <= 240
}

// IsMissingHealth reports the ordinary first-run case without hiding an
// unreadable or malformed queue.
func IsMissingHealth(err error) bool { return errors.Is(err, os.ErrNotExist) }
