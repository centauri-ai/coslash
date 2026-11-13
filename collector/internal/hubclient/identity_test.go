package hubclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPairingSendsComputerIdentityAndRetriesUnknownFields(t *testing.T) {
	const installID = "10000000-0000-4000-8000-000000000201"
	const machineID = "{AABBCCDD-EEFF-0011-2233-445566778899}"
	var beginBodies, tokenBodies []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/v1/device-authorizations":
			beginBodies = append(beginBodies, body)
			if body["installId"] != "" || body["machineId"] != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"code":"invalid_request"}`)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"pair","deviceCode":"device-code","userCode":"USER","verificationUri":"`+serverURL(r)+`/pair","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":5}`)
		case "/v1/device-authorizations/token":
			tokenBodies = append(tokenBodies, body)
			if body["installId"] != "" || body["machineId"] != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"code":"invalid_request"}`)
				return
			}
			_, _ = io.WriteString(w, `{"deviceId":"device","credential":"secret","tokenType":"Device","scope":"ingest"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	credentials := &memoryCredentials{}
	client := &Client{BaseURL: base, Credentials: credentials, HTTP: server.Client(), DeviceName: "Studio.local", InstallID: installID, MachineID: machineID}
	begun, err := client.BeginPairing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PollPairing(context.Background(), begun.PairingID); err != nil {
		t.Fatal(err)
	}
	if credentials.saved != "secret" {
		t.Fatalf("saved credential = %q", credentials.saved)
	}
	if len(beginBodies) != 2 || beginBodies[0]["installId"] != "10000000-0000-4000-8000-000000000201" || beginBodies[0]["machineId"] != "aabbccdd-eeff-0011-2233-445566778899" || beginBodies[0]["deviceName"] != "Studio.local" {
		t.Fatalf("begin bodies = %#v", beginBodies)
	}
	if len(beginBodies[1]) != 1 || beginBodies[1]["deviceName"] != "Studio.local" {
		t.Fatalf("legacy begin body = %#v", beginBodies[1])
	}
	if len(tokenBodies) != 2 || tokenBodies[0]["installId"] == "" || tokenBodies[0]["machineId"] == "" || tokenBodies[0]["deviceCode"] != "device-code" {
		t.Fatalf("token bodies = %#v", tokenBodies)
	}
	if len(tokenBodies[1]) != 1 || tokenBodies[1]["deviceCode"] != "device-code" {
		t.Fatalf("legacy token body = %#v", tokenBodies[1])
	}
	if client.InstallID != installID || client.MachineID != machineID {
		t.Fatal("retry changed the client identity")
	}
}

func TestPairingDoesNotRetryARejectedIdentity(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"pairing_invalid"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &Client{BaseURL: base, Credentials: &memoryCredentials{}, HTTP: server.Client(), InstallID: "10000000-0000-4000-8000-000000000201", MachineID: "aabbccddeeff0011"}
	if _, err := client.BeginPairing(context.Background()); err == nil || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestCheckInSendsComputerIdentityAndRetriesUnknownFields(t *testing.T) {
	const installID = "10000000-0000-4000-8000-000000000201"
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		bodies = append(bodies, body)
		if body["installId"] != nil || body["machineId"] != nil || body["deviceName"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":"invalid_query"}`)
			return
		}
		_, _ = io.WriteString(w, `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"commands":[],"minVersion":"0.0.0","nextCheckInSeconds":60}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &Client{BaseURL: base, Credentials: &memoryCredentials{saved: "credential"}, HTTP: server.Client(), CollectorVersion: "0.4.3", DeviceName: "Studio.local", InstallID: installID, MachineID: "aabbccddeeff0011"}
	if _, err := client.V4CheckIn(context.Background(), V4Queue{}, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CheckIn(context.Background(), "0.4.3"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 4 {
		t.Fatalf("bodies = %d", len(bodies))
	}
	for _, index := range []int{0, 2} {
		if bodies[index]["installId"] != installID || bodies[index]["machineId"] != "aabbccddeeff0011" || bodies[index]["deviceName"] != "Studio.local" {
			t.Fatalf("identity body %d = %#v", index, bodies[index])
		}
	}
	for _, index := range []int{1, 3} {
		if bodies[index]["installId"] != nil || bodies[index]["machineId"] != nil || bodies[index]["deviceName"] != nil {
			t.Fatalf("legacy body %d = %#v", index, bodies[index])
		}
	}
	if client.InstallID != installID || client.MachineID != "aabbccddeeff0011" || client.DeviceName != "Studio.local" {
		t.Fatal("retry changed the client identity")
	}
}

func TestV4CheckInKeepsIdentityWhenOnlyDiagnosticsAreRejected(t *testing.T) {
	var sawIdentityWithDiagnostics, sawIdentityWithoutDiagnostics bool
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			InstallID string `json:"installId"`
			Queue     struct {
				LocalDiagnostics json.RawMessage `json:"localDiagnostics"`
			} `json:"queue"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(body.Queue.LocalDiagnostics) > 0 {
			sawIdentityWithDiagnostics = body.InstallID != ""
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":"invalid_query"}`)
			return
		}
		sawIdentityWithoutDiagnostics = body.InstallID != ""
		_, _ = io.WriteString(w, `{"configVersion":1,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":false},"commands":[],"minVersion":"0.0.0","capabilities":[]}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &Client{BaseURL: base, Credentials: &memoryCredentials{}, HTTP: server.Client(), CollectorVersion: "0.4.3", InstallID: "10000000-0000-4000-8000-000000000201"}
	queue := V4Queue{LocalDiagnostics: &LocalDiagnostics{Version: 1}}
	if _, err := client.V4CheckIn(context.Background(), queue, 0, nil, nil, nil); err != nil || calls != 2 || !sawIdentityWithDiagnostics || !sawIdentityWithoutDiagnostics {
		t.Fatalf("calls=%d with=%t without=%t err=%v", calls, sawIdentityWithDiagnostics, sawIdentityWithoutDiagnostics, err)
	}
	if queue.LocalDiagnostics == nil {
		t.Fatal("retry cleared the caller's diagnostics")
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
