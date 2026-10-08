package hubclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func readT52Fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "t52", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestT52CheckInFixturesDecode(t *testing.T) {
	var request checkInRequest
	if err := json.Unmarshal(readT52Fixture(t, "check-in-policy.json"), &request); err != nil {
		t.Fatalf("decode check-in request fixture: %v", err)
	}
	if request.ClientVersion != "0.0.5" || request.InstallChannel != "script" ||
		request.Queue.Pending != 2 || request.AppliedConfigVersion != 1 {
		t.Fatalf("decoded check-in request = %+v", request)
	}

	var response V4CheckIn
	if err := json.Unmarshal(readT52Fixture(t, "check-in-response-policy.json"), &response); err != nil {
		t.Fatalf("decode check-in response fixture: %v", err)
	}
	if response.ConfigVersion != 1 || response.Config.ImportPlan == nil || response.Config.ImportPlan.Window != "10d" {
		t.Fatalf("decoded check-in response = %+v", response)
	}
}

func TestT52StopCodeFixturesDecodeAsV4Problems(t *testing.T) {
	for _, test := range []struct{ file, code string }{
		{"problem-sync_paused.json", "sync_paused"},
		{"problem-device_sync_off.json", "device_sync_off"},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write(readT52Fixture(t, test.file))
			}))
			defer server.Close()
			base, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := Client{BaseURL: base, Credentials: &memoryCredentials{saved: "device-credential"}, CollectorVersion: "0.0.5"}
			_, err = client.V4CheckIn(context.Background(), V4Queue{}, 0, nil, nil, nil)
			var problem V4Problem
			if !errors.As(err, &problem) || problem.Code != test.code || problem.HTTPStatus != http.StatusConflict {
				t.Fatalf("decoded %s problem = %v", test.code, err)
			}
		})
	}
}
