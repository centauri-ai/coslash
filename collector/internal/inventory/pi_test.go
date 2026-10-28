package inventory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
)

func TestPiInventoryStatsExplicitPlanWithoutReadingSessionContent(t *testing.T) {
	home := t.TempDir()
	agent := filepath.Join(home, "agent")
	custom := filepath.Join(home, "custom sessions")
	for _, dir := range []string{agent, custom} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GROK_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	t.Setenv("COSLASH_PI_SESSION_ROOTS", "")
	t.Setenv("COSLASH_HOME", filepath.Join(home, "coslash"))
	t.Chdir(home)
	settings, err := json.Marshal(map[string]string{"sessionDir": custom})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(custom, "not-the-header-id.jsonl")
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": "custom.header_id", "cwd": home})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, header, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := pi.PlanSourcesContext(t.Context())
	if err != nil || len(plan.Sources) != 1 || plan.Sources[0].ID != "custom.header_id" {
		t.Fatalf("custom-root plan = %+v, %v", plan, err)
	}
	// The stat phase must still count a planned file whose contents no longer parse.
	write(t, path, 37, 0)
	plan.Scan.Files = append(plan.Scan.Files, plan.Scan.Files[0])
	before := append([]string(nil), plan.Scan.Files...)
	options := Options{Home: home, OpenCodeDB: filepath.Join(home, "absent.db"), Now: now, Pi: plan.Scan}
	snapshot, err := Scan(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	report := snapshot.Inventory()
	if report.Files != 1 || report.Bytes != 37 || report.Windows.H24 != windowOf(1, 37) || !reflect.DeepEqual(report.Agents, []hubclient.InventoryAgent{{Agent: vendors.AgentPi, Files: 1, Bytes: 37}}) {
		t.Fatalf("planned Pi inventory = %+v", report)
	}
	if len(snapshot.Files) != 1 || !reflect.DeepEqual(plan.Scan.Files, before) {
		t.Fatalf("stat input mutated or duplicated: %+v", snapshot.Files)
	}
	wire, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(decoded), home) || strings.Contains(fmt.Sprint(decoded), "custom.header_id") {
		t.Fatalf("inventory exposed private identity: %s, %v", wire, err)
	}
	options.Pi = nil
	snapshot, err = Scan(t.Context(), options)
	if err != nil || snapshot.Inventory().Files != 0 {
		t.Fatalf("stat-only Scan discovered Pi implicitly: %+v, %v", snapshot, err)
	}
}

func TestPiInventoryPreservesPlanningFailuresAndRejectsNonFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", "")
	plan := &vendors.SourceScan{Files: []string{filepath.Join(home, "missing.jsonl"), home}, RootMissing: true, SkippedTotal: 3}
	snapshot, err := Scan(t.Context(), Options{Home: home, OpenCodeDB: filepath.Join(home, "absent.db"), Pi: plan})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 0 || snapshot.Skipped != 5 {
		t.Fatalf("unreadable planned paths = %+v", snapshot)
	}
	if !strings.Contains(strings.Join(snapshot.Missing, ","), vendors.AgentPi) {
		t.Fatalf("missing Pi root omitted: %v", snapshot.Missing)
	}
}
