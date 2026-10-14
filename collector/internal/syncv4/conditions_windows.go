package syncv4

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type systemPowerStatus struct {
	acLineStatus, batteryFlag, batteryLifePercent, systemStatusFlag byte
	batteryLifeTime, batteryFullLifeTime                            uint32
}

var getSystemPowerStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemPowerStatus")

// Windows PowerShell cold startup can exceed the Unix probe's two-second budget.
const conditionsTimeout = 10 * time.Second

type windowsNetworkCost struct {
	NetworkCostType               int
	Roaming                       bool
	OverDataLimit                 bool
	ApproachingDataLimit          bool
	BackgroundDataUsageRestricted bool
}

type networkCostProbe struct {
	gate      chan struct{}
	cost      windowsNetworkCost
	checkedAt time.Time
}

var windowsCost = networkCostProbe{gate: make(chan struct{}, 1)}

func (p *networkCostProbe) read(ctx context.Context, now time.Time, probe func(context.Context) (windowsNetworkCost, error)) (windowsNetworkCost, error) {
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return windowsNetworkCost{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return windowsNetworkCost{}, err
	}
	if !p.checkedAt.IsZero() && now.Sub(p.checkedAt) < 15*time.Second {
		return p.cost, nil
	}
	cost, err := probe(ctx)
	if err == nil && ctx.Err() == nil {
		p.cost, p.checkedAt = cost, now
	}
	return cost, err
}

// Run WinRT in a cancellable process: an unavailable network service must not
// leave a COM call blocking the sync worker indefinitely.
const windowsNetworkCostScript = `$ErrorActionPreference = 'Stop'
$profile = [Windows.Networking.Connectivity.NetworkInformation, Windows.Networking.Connectivity, ContentType = WindowsRuntime]::GetInternetConnectionProfile()
if ($null -eq $profile) { exit 1 }
$profile.GetConnectionCost() | ConvertTo-Json -Compress`

func readWindowsPower() (systemPowerStatus, error) {
	var status systemPowerStatus
	ok, _, err := getSystemPowerStatus.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return status, err
	}
	return status, nil
}

func readWindowsNetworkCost(ctx context.Context) (windowsNetworkCost, error) {
	var cost windowsNetworkCost
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		return cost, err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(directory, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", windowsNetworkCostScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	body, err := cmd.Output()
	if ctx.Err() != nil {
		return cost, ctx.Err()
	}
	if err != nil {
		return cost, err
	}
	err = json.Unmarshal(body, &cost)
	return cost, err
}

func platformConditions(ctx context.Context) (bool, int, error) {
	return probeWindowsConditions(ctx, readWindowsPower, func(ctx context.Context) (windowsNetworkCost, error) {
		return windowsCost.read(ctx, time.Now(), readWindowsNetworkCost)
	})
}

func probeWindowsConditions(ctx context.Context, power func() (systemPowerStatus, error), network func(context.Context) (windowsNetworkCost, error)) (bool, int, error) {
	if err := ctx.Err(); err != nil {
		return false, -1, err
	}
	status, err := power()
	if err != nil {
		return false, -1, err
	}
	battery := -1
	if status.acLineStatus != 1 && status.batteryFlag != 128 {
		if status.acLineStatus != 0 || status.batteryLifePercent > 100 {
			return false, -1, ErrPaused
		}
		battery = int(status.batteryLifePercent)
	}
	cost, err := network(ctx)
	if err != nil {
		return false, battery, err
	}
	if err := ctx.Err(); err != nil {
		return false, battery, err
	}
	if cost.Roaming || cost.OverDataLimit || cost.ApproachingDataLimit || cost.BackgroundDataUsageRestricted || cost.NetworkCostType == 2 || cost.NetworkCostType == 3 {
		return true, battery, nil
	}
	if cost.NetworkCostType == 1 {
		return false, battery, nil
	}
	return false, battery, ErrPaused
}
