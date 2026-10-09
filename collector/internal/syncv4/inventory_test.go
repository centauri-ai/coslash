package syncv4

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func TestProgressReportsInventoryOnlyAfterHubAdvertisesScaleImport(t *testing.T) {
	root := t.TempDir()
	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	inventory := hubclient.DeviceInventory{ScannedAt: "2026-09-29T19:29:52Z", Files: 4, Bytes: 40, Agents: []hubclient.InventoryAgent{{Agent: "codex", Files: 4, Bytes: 40}}}
	inventory.Windows.All = hubclient.InventoryWindow{Sessions: 4, Bytes: 40}
	if err := queue.SetInventory(inventory); err != nil {
		t.Fatal(err)
	}
	if queue.Progress().Inventory != nil {
		t.Fatal("inventory reported before the Hub advertised the capability")
	}
	if queue.HubAdvertises(hubclient.CapabilityScaleImport) {
		t.Fatal("capability advertised before any check-in")
	}
	policy := hubclient.V4CheckIn{ConfigVersion: 1, MinVersion: "0.1.0", Capabilities: []string{hubclient.CapabilityScaleImport}}
	if err := queue.ApplyPolicy(policy); err != nil {
		t.Fatal(err)
	}
	progress := queue.Progress()
	if progress.Inventory == nil || progress.Inventory.Windows.All.Sessions != 4 || progress.Inventory.Agents[0].Agent != "codex" {
		t.Fatalf("progress = %+v", progress)
	}
	progress.Inventory.Agents[0].Files = 99
	if queue.Inventory().Agents[0].Files != 4 {
		t.Fatal("progress aliases the queue's inventory")
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Progress().Inventory == nil || !reopened.HubAdvertises(hubclient.CapabilityScaleImport) {
		t.Fatal("inventory and capability did not persist")
	}
	if err := reopened.ApplyPolicy(hubclient.V4CheckIn{ConfigVersion: 2, MinVersion: "0.1.0"}); err != nil {
		t.Fatal(err)
	}
	if reopened.Progress().Inventory != nil {
		t.Fatal("inventory reported after the Hub stopped advertising")
	}
	if err := reopened.ApplyPolicy(policy); err != nil {
		t.Fatal(err)
	}
	policy.ConfigVersion = 3
	policy.Config.LeaveOut = []string{"private/repo"}
	if err := reopened.ApplyPolicy(policy); err != nil {
		t.Fatal(err)
	}
	filtered := reopened.Progress().Inventory
	if filtered == nil || filtered.Windows != (hubclient.InventoryWindows{}) || filtered.Files != 4 {
		t.Fatalf("leave-out inventory = %+v", filtered)
	}
	policy.ConfigVersion = 4
	policy.Config.LeaveOut = nil
	if err := reopened.ApplyPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if reopened.Progress().Inventory.Windows.All.Sessions != 4 {
		t.Fatal("window buckets did not return after leave-out was removed")
	}
	t.Setenv("COSLASH_SCALE_IMPORT", "0")
	if reopened.Progress().Inventory != nil {
		t.Fatal("inventory reported with the kill switch off")
	}
}

func TestQueueWithholdsLegacyInventoryUntilD60Refresh(t *testing.T) {
	root := t.TempDir()
	legacy := `{"version":1,"installId":"legacy","entries":[],"inventory":{"scannedAt":"2026-09-29T19:29:52Z","windows":{"d7":{"sessions":7,"bytes":70},"d10":{"sessions":10,"bytes":100},"d30":{"sessions":30,"bytes":300},"all":{"sessions":40,"bytes":400}}}}`
	if err := os.WriteFile(filepath.Join(root, "queue.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	queue, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Inventory() != nil {
		t.Fatal("legacy inventory was exposed before its 60-day bucket was refreshed")
	}

	fresh := hubclient.DeviceInventory{ScannedAt: "2026-10-01T19:29:52Z"}
	fresh.Windows.D7 = hubclient.InventoryWindow{Sessions: 7, Bytes: 70}
	fresh.Windows.D10 = hubclient.InventoryWindow{Sessions: 10, Bytes: 100}
	fresh.Windows.D60 = hubclient.InventoryWindow{Sessions: 40, Bytes: 400}
	if err := queue.SetInventory(fresh); err != nil {
		t.Fatal(err)
	}
	if got := queue.Inventory(); got == nil || got.Windows.D60 != fresh.Windows.D60 {
		t.Fatalf("refreshed inventory = %+v", got)
	}
}
