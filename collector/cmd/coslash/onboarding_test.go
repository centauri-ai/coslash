package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type onboardingRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper onboardingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func onboardingResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestStoredHubURLContainsOnlyAValidatedHubOrigin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COSLASH_HOME", home)
	if err := writeStoredHubURL("https://beta.coslash.io"); err != nil {
		t.Fatal(err)
	}
	got, err := readStoredHubURL()
	if err != nil || got != "https://beta.coslash.io" {
		t.Fatalf("stored Hub URL=%q error=%v", got, err)
	}
	info, err := os.Stat(filepath.Join(home, storedHubURLFilename))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("Hub address file permissions=%#o", info.Mode().Perm())
	}
	if err := writeStoredHubURL("https://hub.staging.example"); err != nil {
		t.Fatalf("write custom HTTPS Hub origin: %v", err)
	}
	for _, invalid := range []string{"https://beta.coslash.io/path", "http://evil.example"} {
		if err := writeStoredHubURL(invalid); err == nil {
			t.Fatalf("writeStoredHubURL(%q) succeeded", invalid)
		}
	}
}

func TestHubClientFromEnvironmentAllowsExplicitHTTPSHubHosts(t *testing.T) {
	t.Setenv("COSLASH_HUB_URL", "https://hub.staging.example")
	client, err := hubClientFromEnvironment("0.1.0")
	if err != nil || client == nil || client.BaseURL.Host != "hub.staging.example" {
		t.Fatalf("Hub client=%#v error=%v", client, err)
	}
}

func TestOnboardingUpdatesHubDestinationRouteClient(t *testing.T) {
	baseURL, err := url.Parse("https://hub.coslash.io")
	if err != nil {
		t.Fatal(err)
	}
	onboardings := newOnboardingManager("0.1.0")
	defer onboardings.Close()
	api := http.NewServeMux()
	registerHubRoutes(api, nil, nil, nil, onboardings)
	onboardings.setHubClient(&hubclient.Client{
		BaseURL: baseURL, Credentials: fixedHubCredential("device-credential"),
		HTTP: &http.Client{Transport: onboardingRoundTripper(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/share-destination" {
				return onboardingResponse(http.StatusNotFound, ""), nil
			}
			return onboardingResponse(http.StatusOK, `{"contractVersion":"hub-share/v1","state":"ready","destination":{"workspaceId":"workspace","workspaceName":"Team","currentMemberCount":1,"resultingMemberCount":1,"currentApprovedSessionCount":0,"historyDisclosure":"Current members","credentialState":"paired","audienceVersion":"v1"}}`), nil
		})},
	})

	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/hub/destination", nil))
	var destination hubclient.DestinationResult
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &destination) != nil ||
		destination.State != "ready" || !destination.Configured || destination.HubURL != baseURL.String() {
		t.Fatalf("destination status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCurrentHubTransportFollowsOnboardingHub(t *testing.T) {
	manager := newOnboardingManager("0.1.0")
	defer manager.Close()

	var firstRequests, secondRequests []string
	newClient := func(rawURL string, requests *[]string) *hubclient.Client {
		baseURL, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		return &hubclient.Client{
			BaseURL: baseURL, Credentials: fixedHubCredential("device-credential"), CollectorVersion: "0.1.0",
			HTTP: &http.Client{Transport: onboardingRoundTripper(func(request *http.Request) (*http.Response, error) {
				*requests = append(*requests, request.Method+" "+request.URL.Path)
				switch request.URL.Path {
				case "/v4/devices/me/check-in":
					return onboardingResponse(http.StatusOK, `{"configVersion":0,"config":{"paused":false,"deviceOff":false,"leaveOut":[],"agentKnowledge":true},"minVersion":"0.0.0"}`), nil
				case "/v4/devices/me/wait":
					return onboardingResponse(http.StatusOK, `{"configVersion":0,"changed":false,"commandsAvailable":false}`), nil
				default:
					return onboardingResponse(http.StatusNotFound, ""), nil
				}
			})},
		}
	}
	first := newClient("https://hub-a.example", &firstRequests)
	second := newClient("https://hub-b.example", &secondRequests)
	manager.setHubClient(first)
	transport := currentHubTransport{current: manager.currentHubClient}
	manager.setHubClient(second)

	wantBinding, err := second.V4Binding(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := transport.V4Binding(t.Context()); err != nil || got != wantBinding {
		t.Fatalf("sync binding=%q error=%v, want %q", got, err, wantBinding)
	}
	if _, err := transport.V4CheckIn(t.Context(), hubclient.V4Queue{}, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.V4Wait(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if len(firstRequests) != 0 || len(secondRequests) != 2 ||
		secondRequests[0] != "POST /v4/devices/me/check-in" || secondRequests[1] != "GET /v4/devices/me/wait" {
		t.Fatalf("requests to first Hub=%v, second Hub=%v", firstRequests, secondRequests)
	}
}

func TestStartCheckInsAvoidsDuplicatesAndV4Overlap(t *testing.T) {
	baseURL, err := url.Parse("https://hub.coslash.io")
	if err != nil {
		t.Fatal(err)
	}
	client := &hubclient.Client{
		BaseURL: baseURL, Credentials: fixedHubCredential("device-credential"),
		HTTP: &http.Client{Transport: onboardingRoundTripper(func(*http.Request) (*http.Response, error) {
			return onboardingResponse(http.StatusOK, `{"nextCheckInSeconds":300}`), nil
		})},
	}

	t.Run("one worker per Hub", func(t *testing.T) {
		manager := newOnboardingManager("0.1.0")
		manager.StartCheckIns(client)
		manager.StartCheckIns(client)
		manager.mu.Lock()
		active := len(manager.active)
		manager.mu.Unlock()
		manager.Close()
		if active != 1 {
			t.Fatalf("active check-in workers=%d, want 1", active)
		}
	})

	t.Run("v4 owns check-ins", func(t *testing.T) {
		manager := newOnboardingManager("0.1.0")
		manager.SetV4SyncActive(true)
		manager.StartCheckIns(client)
		manager.mu.Lock()
		active := len(manager.active)
		manager.mu.Unlock()
		manager.Close()
		if active != 0 {
			t.Fatalf("active legacy check-in workers=%d, want 0", active)
		}
	})
}
