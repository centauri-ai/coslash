package hubclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseLaunchIntentURL(t *testing.T) {
	const attempt = "10000000-0000-4000-8000-000000000151"
	t.Run("pair activation", func(t *testing.T) {
		parsed, err := ParseLaunchIntentURL("coslash://pair?hub=https%3A%2F%2Fbeta.coslash.io&attempt=" + attempt + "&intent=" + strings.Repeat("A", 43))
		if err != nil || parsed.Action != "pair" || parsed.HubURL.String() != "https://beta.coslash.io" ||
			parsed.AttemptID != attempt || parsed.LaunchToken != strings.Repeat("A", 43) {
			t.Fatalf("intent=%#v err=%v", parsed, err)
		}
	})
	t.Run("first check-in retry", func(t *testing.T) {
		parsed, err := ParseLaunchIntentURL("coslash://check-in?hub=http%3A%2F%2F127.0.0.1%3A8080&attempt=" + attempt)
		if err != nil || parsed.Action != "check-in" || parsed.HubURL.Host != "127.0.0.1:8080" || parsed.AttemptID != attempt {
			t.Fatalf("intent=%#v err=%v", parsed, err)
		}
	})
}

func TestParseLaunchIntentURLRejectsMalformedReplayShapesAndUnsafeHubs(t *testing.T) {
	const attempt = "10000000-0000-4000-8000-000000000151"
	validHub := "https%3A%2F%2Fbeta.coslash.io"
	for _, raw := range []string{
		"coslash://pair?hub=" + validHub + "&attempt=" + attempt, // missing intent
		"coslash://pair?hub=" + validHub + "&attempt=" + attempt + "&intent=short",
		"coslash://pair?hub=" + validHub + "&attempt=" + attempt + "&intent=" + strings.Repeat("A", 43) + "&deviceCode=secret",
		"coslash://pair?hub=" + validHub + "&hub=" + validHub + "&attempt=" + attempt + "&intent=" + strings.Repeat("A", 43),
		"coslash://pair?hub=https%3A%2F%2Fevil.example&attempt=" + attempt + "&intent=" + strings.Repeat("A", 43),
		"coslash://pair?hub=http%3A%2F%2Fevil.example&attempt=" + attempt + "&intent=" + strings.Repeat("A", 43),
		"coslash://unknown?hub=" + validHub + "&attempt=" + attempt,
		"coslash://check-in?hub=" + validHub + "&attempt=" + attempt + "&intent=not-allowed",
		"coslash://pair/path?hub=" + validHub + "&attempt=" + attempt + "&intent=" + strings.Repeat("A", 43),
		"coslash://pair?hub=" + validHub + "&attempt=invalid&intent=" + strings.Repeat("A", 43),
		"coslash://pair?hub=" + validHub + "&attempt=" + attempt + "&intent=" + strings.Repeat("A", 43) + "#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseLaunchIntentURL(raw); err == nil {
				t.Fatalf("ParseLaunchIntentURL(%q) succeeded", raw)
			}
		})
	}
}

func TestValidateHubURL(t *testing.T) {
	for _, raw := range []string{
		"https://beta.coslash.io",
		"https://hub.coslash.io",
		"https://hub.staging.example",
		"http://localhost:8080",
		"http://[::1]:8080",
	} {
		if _, err := ValidateHubURL(raw); err != nil {
			t.Fatalf("ValidateHubURL(%q): %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://example.com",
		"https://user@beta.coslash.io",
	} {
		if _, err := ValidateHubURL(raw); err == nil {
			t.Fatalf("ValidateHubURL(%q) succeeded", raw)
		}
	}
	for _, raw := range []string{"https://hub.staging.example", "https://coslash.io.evil.example"} {
		if _, err := ValidateActivationHubURL(raw); err == nil {
			t.Fatalf("ValidateActivationHubURL(%q) succeeded", raw)
		}
	}
}

func TestBeginOnboardingPairingKeepsAuthorizationMaterialInsideLocal(t *testing.T) {
	const attempt = "10000000-0000-4000-8000-000000000151"
	const intent = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	credentials := &memoryCredentials{}
	var claimBody map[string]string
	claimRequests := 0
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		claimRequests++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/device-onboardings/"+attempt+"/claim" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&claimBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"10000000-0000-4000-8000-000000000152","deviceCode":"private-device-code","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":2}`))
	}))
	defer hub.Close()
	base, _ := url.Parse(hub.URL)
	client := Client{BaseURL: base, Credentials: credentials, DeviceName: "Nia’s Mac", CollectorVersion: "0.2.0-x", HTTP: hub.Client()}
	pairing, err := client.BeginOnboardingPairing(context.Background(), attempt, intent)
	if err != nil || pairing.State != "pending" || pairing.UserCode != "" || pairing.PairingID != "10000000-0000-4000-8000-000000000152" {
		t.Fatalf("pairing=%#v error=%v", pairing, err)
	}
	if claimBody["launchIntent"] != intent || claimBody["deviceName"] != "Nia’s Mac" ||
		claimBody["os"] != runtime.GOOS || claimBody["arch"] != runtime.GOARCH || claimBody["version"] != "0.2.0-x" || len(claimBody) != 5 {
		t.Fatalf("claim body=%#v", claimBody)
	}
	if claimRequests != 1 {
		t.Fatalf("modern Hub claim requests=%d, want 1", claimRequests)
	}
	client.pairingMu.Lock()
	secret, ok := client.pairings[pairing.PairingID]
	client.pairingMu.Unlock()
	if !ok || secret.deviceCode != "private-device-code" || secret.credential != "" {
		t.Fatalf("local pairing secret=%#v found=%t", secret, ok)
	}
}

func TestOnboardingDeviceFactsOmitsUnknownOrInvalidVersion(t *testing.T) {
	for _, version := range []string{"", "not-a-version"} {
		facts := (&Client{CollectorVersion: version}).onboardingDeviceFacts()
		if facts.OS != runtime.GOOS || facts.Arch != runtime.GOARCH || facts.Version != "" {
			t.Fatalf("version=%q device facts=%#v", version, facts)
		}
	}
}

func TestClaimOnboardingCodeKeepsCodeAndAuthorizationMaterialInMemory(t *testing.T) {
	credentials := &memoryCredentials{}
	var claimBody map[string]string
	claimRequests := 0
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		claimRequests++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/device-onboarding-codes/claim" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&claimBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"10000000-0000-4000-8000-000000000152","deviceCode":"private-device-code","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":2}`))
	}))
	defer hub.Close()
	base, _ := url.Parse(hub.URL)
	client := Client{BaseURL: base, Credentials: credentials, DeviceName: "Nia’s Mac", CollectorVersion: "0.2.0-x", HTTP: hub.Client()}
	pairing, err := client.ClaimOnboardingCode(context.Background(), "k7qx-29pd")
	if err != nil || pairing.State != "pending" || pairing.PairingID != "10000000-0000-4000-8000-000000000152" {
		t.Fatalf("pairing=%#v error=%v", pairing, err)
	}
	if claimBody["connectCode"] != "K7QX-29PD" || claimBody["deviceName"] != "Nia’s Mac" ||
		claimBody["os"] != runtime.GOOS || claimBody["arch"] != runtime.GOARCH || claimBody["version"] != "0.2.0-x" || len(claimBody) != 5 {
		t.Fatalf("claim body=%#v", claimBody)
	}
	if claimRequests != 1 {
		t.Fatalf("modern Hub claim requests=%d, want 1", claimRequests)
	}
	client.pairingMu.Lock()
	secret, ok := client.pairings[pairing.PairingID]
	client.pairingMu.Unlock()
	if !ok || secret.deviceCode != "private-device-code" || secret.credential != "" {
		t.Fatalf("local pairing secret=%#v found=%t", secret, ok)
	}
}

func TestOnboardingClaimsFallBackWhenHubRejectsDeviceDetails(t *testing.T) {
	previousLogger := slog.Default()
	var logs strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)

	tests := []struct {
		name        string
		path        string
		legacyKey   string
		legacyValue string
		claim       func(*Client) (PairingResult, error)
	}{
		{
			name:      "launch intent",
			path:      "/v1/device-onboardings/10000000-0000-4000-8000-000000000151/claim",
			legacyKey: "launchIntent", legacyValue: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			claim: func(client *Client) (PairingResult, error) {
				return client.BeginOnboardingPairing(context.Background(), "10000000-0000-4000-8000-000000000151", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
			},
		},
		{
			name:      "connect code",
			path:      "/v1/device-onboarding-codes/claim",
			legacyKey: "connectCode", legacyValue: "K7QX-29PD",
			claim: func(client *Client) (PairingResult, error) {
				return client.ClaimOnboardingCode(context.Background(), "K7QX-29PD")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests int
			var bodies []map[string]json.RawMessage
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests++
				if request.Method != http.MethodPost || request.URL.Path != test.path {
					t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				bodies = append(bodies, body)
				if _, hasDeviceDetails := body["os"]; hasDeviceDetails {
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"code":"invalid_request"}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":"10000000-0000-4000-8000-000000000152","deviceCode":"private-device-code","expiresAt":"2099-01-01T00:00:00Z","intervalSeconds":2}`))
			}))
			defer hub.Close()
			base, _ := url.Parse(hub.URL)
			client := Client{BaseURL: base, Credentials: &memoryCredentials{}, DeviceName: "Nia’s Mac", CollectorVersion: "0.2.0", HTTP: hub.Client()}
			pairing, err := test.claim(&client)
			if err != nil || pairing.State != "pending" {
				t.Fatalf("pairing=%#v error=%v", pairing, err)
			}
			if requests != 2 || len(bodies) != 2 {
				t.Fatalf("claim requests=%d bodies=%d, want exactly 2", requests, len(bodies))
			}
			if _, ok := bodies[0]["os"]; !ok {
				t.Fatalf("first claim omitted device details: %#v", bodies[0])
			}
			if len(bodies[1]) != 2 || string(bodies[1][test.legacyKey]) != `"`+test.legacyValue+`"` || string(bodies[1]["deviceName"]) != `"Nia’s Mac"` {
				t.Fatalf("fallback claim body=%s, want only %s and deviceName", bodies[1], test.legacyKey)
			}
		})
	}
	const fallbackLog = "Hub doesn't accept device details yet; connecting without them."
	if count := strings.Count(logs.String(), fallbackLog); count != len(tests) || !strings.Contains(logs.String(), "level=INFO") {
		t.Fatalf("fallback logs=%q, want %d info messages", logs.String(), len(tests))
	}
}

func TestOnboardingClaimsDoNotRetryOtherErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		claim  func(*Client) (PairingResult, error)
	}{
		{
			name: "launch intent invalid query", status: http.StatusBadRequest, code: "invalid_query",
			claim: func(client *Client) (PairingResult, error) {
				return client.BeginOnboardingPairing(context.Background(), "10000000-0000-4000-8000-000000000151", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
			},
		},
		{
			name: "connect code server error", status: http.StatusInternalServerError, code: "invalid_request",
			claim: func(client *Client) (PairingResult, error) {
				return client.ClaimOnboardingCode(context.Background(), "K7QX-29PD")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(test.status)
				_, _ = fmt.Fprintf(w, `{"code":%q}`, test.code)
			}))
			defer hub.Close()
			base, _ := url.Parse(hub.URL)
			client := Client{BaseURL: base, Credentials: &memoryCredentials{}, HTTP: hub.Client()}
			if _, err := test.claim(&client); err == nil {
				t.Fatal("claim succeeded for error response")
			}
			if requests != 1 {
				t.Fatalf("claim requests=%d, want 1", requests)
			}
		})
	}
}

func TestClaimOnboardingCodeDistinguishesInvalidAndUnsupported(t *testing.T) {
	for _, test := range []struct {
		name    string
		problem string
		want    error
	}{
		{name: "invalid code", problem: "connect_code_invalid", want: ErrConnectCodeInvalid},
		{name: "old Hub", problem: "not_found", want: ErrConnectUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprintf(w, `{"code":%q}`, test.problem)
			}))
			defer hub.Close()
			base, _ := url.Parse(hub.URL)
			client := Client{BaseURL: base, Credentials: &memoryCredentials{}, HTTP: hub.Client()}
			_, err := client.ClaimOnboardingCode(context.Background(), "K7QX-29PD")
			if !errors.Is(err, test.want) {
				t.Fatalf("ClaimOnboardingCode error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestClaimOnboardingCodeRejectsMalformedCodeBeforeRequest(t *testing.T) {
	called := false
	hub := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer hub.Close()
	base, _ := url.Parse(hub.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, HTTP: hub.Client()}
	if _, err := client.ClaimOnboardingCode(context.Background(), "K7QX-29PI"); err == nil || called {
		t.Fatalf("ClaimOnboardingCode malformed code error=%v called=%t", err, called)
	}
}

func TestConnectPairingReportsDeclinedApproval(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/device-authorizations/token" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"onboarding_declined"}`))
	}))
	defer hub.Close()
	base, _ := url.Parse(hub.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{}, HTTP: hub.Client(), pairings: map[string]pairingSecret{
		"10000000-0000-4000-8000-000000000152": {deviceCode: "device-code", expiresAt: time.Now().Add(time.Minute)},
	}}
	result, err := client.PollPairing(context.Background(), "10000000-0000-4000-8000-000000000152")
	if err != nil || result.State != "declined" {
		t.Fatalf("pairing result=%#v error=%v", result, err)
	}
}

func TestCheckInSendsOnlyContentFreeDeviceStateWithStoredCredential(t *testing.T) {
	credentials := &memoryCredentials{saved: "device-credential"}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v4/devices/me/check-in" ||
			request.Header.Get("Authorization") != "Device credential" {
			t.Fatalf("unexpected check-in request %s %s authorization=%q", request.Method, request.URL.Path, request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"sessions", "content", "deviceCode", "launchIntent", "credential", "localApiToken"} {
			if _, exists := body[forbidden]; exists {
				t.Fatalf("check-in included %q: %#v", forbidden, body)
			}
		}
		if body["clientVersion"] != "1.2.3" || body["os"] == "" {
			t.Fatalf("check-in identity=%#v", body)
		}
		queue, ok := body["queue"].(map[string]any)
		if !ok || queue["pending"] != float64(0) {
			t.Fatalf("check-in queue=%#v", body["queue"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readT52Fixture(t, "check-in-response-policy.json"))
	}))
	defer hub.Close()
	base, _ := url.Parse(hub.URL)
	client := Client{BaseURL: base, Credentials: credentials, HTTP: hub.Client()}
	interval, err := client.CheckIn(context.Background(), "1.2.3")
	if err != nil || interval != 30*time.Second {
		t.Fatalf("interval=%s error=%v", interval, err)
	}
}

func TestLegacyCheckInReportsNormalizedVersion(t *testing.T) {
	var seen checkInRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/devices/me/check-in" {
			t.Fatalf("check-in path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(readT52Fixture(t, "check-in-response-policy.json"))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{saved: "device-credential"}, InstallChannel: "script"}
	if _, err := client.CheckIn(context.Background(), "1.2.3+build.7"); err != nil {
		t.Fatal(err)
	}
	if seen.ClientVersion != "1.2.3+build.7" {
		t.Fatalf("legacy check-in identity = %+v", seen)
	}
}

func TestLegacyCheckInReportsGitDescribeVersion(t *testing.T) {
	const version = "0.2.0-122-g06c5ddc3-dirty"
	var seen checkInRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(readT52Fixture(t, "check-in-response-policy.json"))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := Client{BaseURL: base, Credentials: &memoryCredentials{saved: "device-credential"}, HTTP: server.Client()}
	if _, err := client.CheckIn(context.Background(), version); err != nil {
		t.Fatal(err)
	}
	if seen.ClientVersion != version {
		t.Fatalf("git describe check-in version=%q, want %q", seen.ClientVersion, version)
	}
}

func TestCheckInClassifiesRejectedClientVersion(t *testing.T) {
	for _, test := range []struct {
		name, detail string
		wantRejected bool
	}{
		{name: "version field", detail: "clientVersion must be valid SemVer", wantRejected: true},
		{name: "other field", detail: "queue.pending is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprintf(w, `{"code":"invalid_query","detail":%q}`, test.detail)
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			client := Client{BaseURL: base, Credentials: &memoryCredentials{saved: "device-credential"}, HTTP: server.Client()}
			_, err := client.CheckIn(context.Background(), "1.2.3")
			if errors.Is(err, ErrClientVersionRejected) != test.wantRejected {
				t.Fatalf("check-in error=%v, rejected=%t; want rejected=%t", err, errors.Is(err, ErrClientVersionRejected), test.wantRejected)
			}
		})
	}
}

func TestLegacyCheckInDeletesRevokedCredential(t *testing.T) {
	credentials := &memoryCredentials{}
	base, _ := url.Parse("https://hub.example")
	client := Client{BaseURL: base, Credentials: credentials, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusForbidden, `{"code":"device_revoked"}`), nil
	})}}

	_, err := client.CheckIn(context.Background(), "1.2.3")
	var problem V4Problem
	if !errors.As(err, &problem) || problem.Code != "device_revoked" || !credentials.deleted {
		t.Fatalf("revoked check-in error=%v deleted=%t", err, credentials.deleted)
	}
	if _, err := client.CheckIn(context.Background(), "1.2.3"); !errors.Is(err, ErrCredentialStoreUnavailable) {
		t.Fatalf("check-in after revocation error=%v, want unavailable credentials", err)
	}
}

type revokedCheckInCredentials struct {
	cleanupIsBounded bool
}

func (*revokedCheckInCredentials) Load(context.Context) (string, error) { return "credential", nil }
func (*revokedCheckInCredentials) Save(context.Context, string) error   { return nil }
func (s *revokedCheckInCredentials) Delete(ctx context.Context) error {
	s.captureCleanupContext(ctx)
	return nil
}

func (s *revokedCheckInCredentials) captureCleanupContext(ctx context.Context) {
	deadline, ok := ctx.Deadline()
	remaining := time.Until(deadline)
	s.cleanupIsBounded = ok && ctx.Err() == nil && remaining > 0 && remaining <= 30*time.Second
}

type conditionalRevokedCheckInCredentials struct {
	*revokedCheckInCredentials
	deleteErr error
}

func (s *conditionalRevokedCheckInCredentials) DeleteIfMatches(ctx context.Context, _ string) (bool, error) {
	s.captureCleanupContext(ctx)
	return s.deleteErr == nil, s.deleteErr
}

func TestLegacyCheckInBoundsRevokedCredentialCleanup(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		conditional bool
	}{{name: "delete"}, {name: "delete-if-matches", conditional: true}} {
		t.Run(testCase.name, func(t *testing.T) {
			credentials := &revokedCheckInCredentials{}
			var store CredentialStore = credentials
			if testCase.conditional {
				store = &conditionalRevokedCheckInCredentials{revokedCheckInCredentials: credentials}
			}
			base, _ := url.Parse("https://hub.example")
			client := Client{BaseURL: base, Credentials: store, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusForbidden, `{"code":"device_revoked"}`), nil
			})}}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := client.CheckIn(ctx, "1.2.3"); err == nil {
				t.Fatal("check-in succeeded with a revoked credential")
			}
			if !credentials.cleanupIsBounded {
				t.Fatal("cleanup context was canceled or lacked a 30-second deadline")
			}
		})
	}
}

func TestLegacyCheckInReportsRevokedCredentialCleanupFailure(t *testing.T) {
	base, _ := url.Parse("https://hub.example")
	credentials := &conditionalRevokedCheckInCredentials{
		revokedCheckInCredentials: &revokedCheckInCredentials{},
		deleteErr:                 context.DeadlineExceeded,
	}
	client := Client{BaseURL: base, Credentials: credentials, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusForbidden, `{"code":"device_revoked"}`), nil
	})}}

	_, err := client.CheckIn(context.Background(), "1.2.3")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("check-in error = %v, want cleanup deadline error", err)
	}
}

func TestClaimOnboardingCodePreservesTemporaryCause(t *testing.T) {
	for _, test := range []struct {
		status int
		header string
		code   string
		delay  int
	}{
		{429, "60", "rate_limited", 60},
		{503, "Wed, 01 Jan 2200 00:00:00 GMT", "hub_temporarily_unavailable", 86400},
		{500, "Sat, 01 Jan 2000 00:00:00 GMT", "hub_temporarily_unavailable", 0},
	} {
		t.Run(fmt.Sprintf("%d", test.status), func(t *testing.T) {
			calls := 0
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Retry-After", test.header)
				w.WriteHeader(test.status)
				fmt.Fprint(w, `{"code":"untrusted","detail":"secret"}`)
			}))
			defer hub.Close()
			base, _ := url.Parse(hub.URL)
			client := Client{BaseURL: base, Credentials: &memoryCredentials{}, HTTP: hub.Client()}
			_, err := client.ClaimOnboardingCode(context.Background(), "K7QX-29PD")
			var cause ConnectClaimError
			if !errors.As(err, &cause) || cause.Code != test.code || cause.RetryAfterSeconds != test.delay || calls != 1 {
				t.Fatalf("cause=%#v err=%v calls=%d", cause, err, calls)
			}
		})
	}
}
