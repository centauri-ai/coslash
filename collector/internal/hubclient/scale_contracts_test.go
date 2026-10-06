package hubclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// The fixtures are the scale-contracts/v1 golden files pinned by the server
// at b1345ed (testdata/scale-contracts-v1/SHA256SUMS); the hashes are
// repeated here so a silent edit on either side is caught.
var scaleFixtureHashes = map[string]string{
	"check-in-import-progress.json":         "d960aa5fc7693058d66682736c4e30b4fd8104859f836a60d7aa070caf60b1fd",
	"check-in-inventory-unknown-agent.json": "3c0fc797e351a2969471149a4ed98dff9dea54501fd519db27616f283a417ce7",
	"check-in-inventory-with-path.json":     "efe6cb8f2219dfc7bc10bb5c31e98ccab6ddc0a7e66a0409af4009f7f164ea56",
	"check-in-inventory-with-title.json":    "192a80ca9c5f263bf6b45248ef4b977a7666658288aaa34683e1b0282149f011",
	"check-in-response-import-plan.json":    "2ac857f153f5ed5861cf05f7e3fdd5faf5659530863a4afa94501e3b570ee025",
}

func readScaleFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "scale-contracts-v1", name))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != scaleFixtureHashes[name] {
		t.Fatalf("%s hash = %s, want the pinned %s", name, got, scaleFixtureHashes[name])
	}
	return data
}

func fixtureInventory(t *testing.T, name string) json.RawMessage {
	t.Helper()
	var checkIn struct {
		Queue struct {
			Inventory json.RawMessage `json:"inventory"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(readScaleFixture(t, name), &checkIn); err != nil {
		t.Fatal(err)
	}
	return checkIn.Queue.Inventory
}

func TestDeviceInventoryMirrorsTheGoldenFixture(t *testing.T) {
	raw := fixtureInventory(t, "check-in-import-progress.json")
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var inventory DeviceInventory
	if err := decoder.Decode(&inventory); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("mirror drifts from the fixture:\n%s\n%s", wantJSON, gotJSON)
	}
	if inventory.Windows.All.Sessions != 3712 || len(inventory.Agents) != 4 || inventory.Agents[0].Agent != "codex" {
		t.Fatalf("decoded inventory = %+v", inventory)
	}
}

// The mirror carries no field that could hold content, so the invalid
// fixtures with a path or title are rejected before they reach the wire.
func TestDeviceInventoryRejectsContentBearingFixtures(t *testing.T) {
	for _, name := range []string{"check-in-inventory-with-path.json", "check-in-inventory-with-title.json"} {
		decoder := json.NewDecoder(bytes.NewReader(fixtureInventory(t, name)))
		decoder.DisallowUnknownFields()
		var inventory DeviceInventory
		if err := decoder.Decode(&inventory); err == nil {
			t.Fatalf("%s decoded into the content-free mirror", name)
		}
	}
}

func TestV4CheckInResponseCarriesHubCapabilities(t *testing.T) {
	var response V4CheckIn
	if err := json.Unmarshal(readScaleFixture(t, "check-in-response-import-plan.json"), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Capabilities) != 1 || response.Capabilities[0] != CapabilityScaleImport || response.ConfigVersion != 9 {
		t.Fatalf("response = %+v", response)
	}
}

func TestV4CheckInAdvertisesScaleImportAndSendsInventory(t *testing.T) {
	inventory := DeviceInventory{ScannedAt: "2026-09-29T19:29:52Z", DurationMs: 12, Files: 3, Bytes: 30, Agents: []InventoryAgent{{Agent: "claude", Files: 3, Bytes: 30}}}
	inventory.Windows.All = InventoryWindow{Sessions: 2, Bytes: 30}
	var body map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.1.0","recommendedVersion":"0.1.0","nextCheckInSeconds":300,"capabilities":["scale-import/v1"]}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, CollectorVersion: "0.1.0"}
	queue := V4Queue{Inventory: &inventory}
	queue.FirstSync.HistoryState = "complete"
	result, err := client.V4CheckIn(context.Background(), queue, 0, nil, []string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Capabilities) != 1 || result.Capabilities[0] != CapabilityScaleImport {
		t.Fatalf("hub capabilities = %v", result.Capabilities)
	}
	var capabilities []string
	if err := json.Unmarshal(body["capabilities"], &capabilities); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, capability := range capabilities {
		found = found || capability == CapabilityScaleImport
	}
	if !found {
		t.Fatalf("capabilities sent = %v", capabilities)
	}
	var sent struct {
		Inventory *DeviceInventory `json:"inventory"`
	}
	if err := json.Unmarshal(body["queue"], &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Inventory == nil || sent.Inventory.Windows.All.Sessions != 2 {
		t.Fatalf("queue sent = %s", body["queue"])
	}

	queue.Inventory = nil
	if _, err := client.V4CheckIn(context.Background(), queue, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body["queue"], []byte("inventory")) {
		t.Fatalf("inventory sent while unknown: %s", body["queue"])
	}

	t.Setenv("COSLASH_SCALE_IMPORT", "0")
	if _, err := client.V4CheckIn(context.Background(), queue, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body["capabilities"], []byte(CapabilityScaleImport)) {
		t.Fatalf("capability advertised with the kill switch off: %s", body["capabilities"])
	}
}
