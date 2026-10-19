package syncv4

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWindowsConditions(t *testing.T) {
	for _, test := range []struct {
		name    string
		power   systemPowerStatus
		cost    windowsNetworkCost
		metered bool
		battery int
		paused  bool
	}{
		{"AC unknown battery", systemPowerStatus{acLineStatus: 1, batteryFlag: 255, batteryLifePercent: 255}, windowsNetworkCost{NetworkCostType: 1}, false, -1, false},
		{"desktop", systemPowerStatus{acLineStatus: 255, batteryFlag: 128, batteryLifePercent: 255}, windowsNetworkCost{NetworkCostType: 1}, false, -1, false},
		{"low battery", systemPowerStatus{batteryLifePercent: 19}, windowsNetworkCost{NetworkCostType: 1}, false, 19, false},
		{"battery threshold", systemPowerStatus{batteryLifePercent: 20}, windowsNetworkCost{NetworkCostType: 1}, false, 20, false},
		{"metered", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{NetworkCostType: 2}, true, -1, false},
		{"variable", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{NetworkCostType: 3}, true, -1, false},
		{"roaming", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{NetworkCostType: 1, Roaming: true}, true, -1, false},
		{"over limit", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{NetworkCostType: 1, OverDataLimit: true}, true, -1, false},
		{"near limit", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{NetworkCostType: 1, ApproachingDataLimit: true}, true, -1, false},
		{"background restricted", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{NetworkCostType: 1, BackgroundDataUsageRestricted: true}, true, -1, false},
		{"offline", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{}, false, -1, true},
		{"unknown cost", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{}, false, -1, true},
		{"invalid cost", systemPowerStatus{acLineStatus: 1}, windowsNetworkCost{NetworkCostType: 4}, false, -1, true},
		{"unknown battery", systemPowerStatus{batteryLifePercent: 255}, windowsNetworkCost{NetworkCostType: 1}, false, -1, true},
		{"unknown AC", systemPowerStatus{acLineStatus: 255, batteryLifePercent: 80}, windowsNetworkCost{NetworkCostType: 1}, false, -1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			metered, battery, err := probeWindowsConditions(context.Background(), func() (systemPowerStatus, error) { return test.power, nil }, func(context.Context) (windowsNetworkCost, error) { return test.cost, nil })
			if metered != test.metered || battery != test.battery || errors.Is(err, ErrPaused) != test.paused {
				t.Fatalf("got (%v, %d, %v)", metered, battery, err)
			}
		})
	}
}

func TestWindowsNetworkCostCache(t *testing.T) {
	p := networkCostProbe{gate: make(chan struct{}, 1)}
	now := time.Unix(100, 0)
	calls := 0
	probe := func(context.Context) (windowsNetworkCost, error) {
		calls++
		return windowsNetworkCost{NetworkCostType: calls}, nil
	}
	for _, offset := range []time.Duration{0, 14 * time.Second, 15 * time.Second} {
		_, err := p.read(context.Background(), now.Add(offset), probe)
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("probe calls=%d", calls)
	}
	_, err := p.read(context.Background(), now.Add(30*time.Second), func(context.Context) (windowsNetworkCost, error) { return windowsNetworkCost{}, ErrPaused })
	if !errors.Is(err, ErrPaused) {
		t.Fatal(err)
	}
	cost, err := p.read(context.Background(), now.Add(30*time.Second), probe)
	if err != nil || cost.NetworkCostType != 3 {
		t.Fatalf("failed refresh reused stale cost: %v, %v", cost, err)
	}
	p.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.read(ctx, now, probe)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-p.gate
}

func TestWindowsConditionsErrorsAndCancellation(t *testing.T) {
	probeError := errors.New("probe failed")
	power := func() (systemPowerStatus, error) { return systemPowerStatus{acLineStatus: 1}, nil }
	network := func(context.Context) (windowsNetworkCost, error) { return windowsNetworkCost{NetworkCostType: 1}, nil }
	_, _, err := probeWindowsConditions(context.Background(), func() (systemPowerStatus, error) { return systemPowerStatus{}, probeError }, network)
	if !errors.Is(err, probeError) {
		t.Fatal(err)
	}
	_, _, err = probeWindowsConditions(context.Background(), power, func(context.Context) (windowsNetworkCost, error) { return windowsNetworkCost{}, probeError })
	if !errors.Is(err, probeError) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = probeWindowsConditions(ctx, func() (systemPowerStatus, error) {
		t.Fatal("power called after cancellation")
		return systemPowerStatus{}, nil
	}, network)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	_, _, err = probeWindowsConditions(ctx, power, func(context.Context) (windowsNetworkCost, error) {
		cancel()
		return windowsNetworkCost{NetworkCostType: 1}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLocalConditionsOverrides(t *testing.T) {
	t.Setenv("COSLASH_SYNC_METERED", "")
	t.Setenv("COSLASH_SYNC_OFFLINE", "")
	t.Setenv("COSLASH_SYNC_BATTERY_PERCENT", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name, value string
		metered     bool
		battery     int
		paused      bool
	}{
		{"COSLASH_SYNC_METERED", "1", true, -1, false},
		{"COSLASH_SYNC_OFFLINE", "1", false, -1, true},
		{"COSLASH_SYNC_BATTERY_PERCENT", "19", false, 19, false},
		{"COSLASH_SYNC_BATTERY_PERCENT", "101", false, -1, true},
	} {
		t.Run(test.name+test.value, func(t *testing.T) {
			t.Setenv(test.name, test.value)
			metered, battery, err := LocalConditions(ctx)
			if metered != test.metered || battery != test.battery || errors.Is(err, ErrPaused) != test.paused {
				t.Fatalf("got (%v, %d, %v)", metered, battery, err)
			}
		})
	}
}
