package hubclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var (
	onboardingUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	launchToken    = regexp.MustCompile(`^[A-Za-z0-9_-]{40,128}$`)
	clientVersion  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	connectCode    = regexp.MustCompile(`(?i)^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)
)

var (
	ErrConnectCodeInvalid = errors.New("connect_code_invalid")
	ErrConnectUnsupported = errors.New("connect_unsupported")
)

func ValidConnectCode(code string) bool { return connectCode.MatchString(code) }

type LaunchIntent struct {
	Action      string
	HubURL      *url.URL
	AttemptID   string
	LaunchToken string
}

// ParseLaunchIntentURL accepts only the two application actions used by Hub.
// The launch token is an opaque, one-time bearer value; no device secret is
// accepted in an activation URL.
func ParseLaunchIntentURL(raw string) (LaunchIntent, error) {
	if len(raw) == 0 || len(raw) > 2048 {
		return LaunchIntent{}, errors.New("invalid coSlash activation URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "coslash" || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" || parsed.Path != "" {
		return LaunchIntent{}, errors.New("invalid coSlash activation URL")
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return LaunchIntent{}, errors.New("invalid coSlash activation URL")
	}
	for key, entries := range values {
		if len(entries) != 1 || (key != "hub" && key != "attempt" && key != "intent") {
			return LaunchIntent{}, errors.New("invalid coSlash activation URL")
		}
	}
	if values.Get("hub") == "" || !onboardingUUID.MatchString(values.Get("attempt")) {
		return LaunchIntent{}, errors.New("invalid coSlash activation URL")
	}
	hub, err := ValidateActivationHubURL(values.Get("hub"))
	if err != nil || hub.Path != "" || hub.RawPath != "" || hub.RawQuery != "" || hub.Fragment != "" {
		return LaunchIntent{}, errors.New("unsupported Hub address")
	}
	switch parsed.Host {
	case "pair":
		intent := values.Get("intent")
		if !launchToken.MatchString(intent) {
			return LaunchIntent{}, errors.New("invalid coSlash activation URL")
		}
		return LaunchIntent{Action: "pair", HubURL: hub, AttemptID: values.Get("attempt"), LaunchToken: intent}, nil
	case "check-in":
		if values.Has("intent") {
			return LaunchIntent{}, errors.New("invalid coSlash activation URL")
		}
		return LaunchIntent{Action: "check-in", HubURL: hub, AttemptID: values.Get("attempt")}, nil
	default:
		return LaunchIntent{}, errors.New("unsupported coSlash activation action")
	}
}

// ValidateHubURL validates a Hub URL explicitly configured by the user.
func ValidateHubURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("Hub address must be an absolute HTTP(S) URL without credentials")
	}
	if parsed.Scheme == "http" {
		host := strings.ToLower(parsed.Hostname())
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return nil, errors.New("Hub address requires HTTPS outside loopback development")
		}
	}
	return parsed, nil
}

// ValidateActivationHubURL prevents an untrusted activation intent from
// redirecting Local's bearer intent to arbitrary hosts.
func ValidateActivationHubURL(raw string) (*url.URL, error) {
	parsed, err := ValidateHubURL(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "https" {
		host := strings.ToLower(parsed.Hostname())
		if host != "coslash.io" && !strings.HasSuffix(host, ".coslash.io") {
			return nil, errors.New("Hub address is not a supported coSlash host")
		}
	}
	return parsed, nil
}

type onboardingClaimResponse struct {
	ID              string    `json:"id"`
	DeviceCode      string    `json:"deviceCode"`
	ExpiresAt       time.Time `json:"expiresAt"`
	IntervalSeconds int       `json:"intervalSeconds"`
}

func (c *Client) BeginOnboardingPairing(ctx context.Context, attemptID, launchIntent string) (PairingResult, error) {
	if !c.configured() || !onboardingUUID.MatchString(attemptID) || !launchToken.MatchString(launchIntent) {
		return PairingResult{}, errors.New("Hub onboarding is not configured")
	}
	body, err := json.Marshal(map[string]string{"launchIntent": launchIntent, "deviceName": c.DeviceName})
	if err != nil {
		return PairingResult{}, errors.New("could not prepare Hub onboarding")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint("/v1/device-onboardings/"+url.PathEscape(attemptID)+"/claim"), bytes.NewReader(body))
	if err != nil {
		return PairingResult{}, errors.New("could not prepare Hub onboarding")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return PairingResult{}, errors.New("Hub could not receive the Local pairing request")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		problem := readProblem(response)
		return PairingResult{}, fmt.Errorf("Hub could not start pairing: %s", problem.Code)
	}
	var authorization onboardingClaimResponse
	if err := decodeBounded(response.Body, &authorization); err != nil || !onboardingUUID.MatchString(authorization.ID) ||
		authorization.DeviceCode == "" || len(authorization.DeviceCode) > 256 ||
		!authorization.ExpiresAt.After(time.Now()) || authorization.IntervalSeconds < 1 || authorization.IntervalSeconds > 30 {
		return PairingResult{}, errors.New("Hub returned an invalid onboarding response")
	}
	c.pairingMu.Lock()
	if c.pairings == nil {
		c.pairings = make(map[string]pairingSecret)
	}
	c.pairings[authorization.ID] = pairingSecret{deviceCode: authorization.DeviceCode, expiresAt: authorization.ExpiresAt}
	c.pairingMu.Unlock()
	return PairingResult{State: "pending", PairingID: authorization.ID,
		ExpiresAt: authorization.ExpiresAt, IntervalSeconds: authorization.IntervalSeconds}, nil
}

// ClaimOnboardingCode claims the single-use setup request from the command the
// person pasted into Terminal. The code is sent only in the request body and is
// never retained by this client after the request completes.
func (c *Client) ClaimOnboardingCode(ctx context.Context, code string) (PairingResult, error) {
	if !c.configured() || !ValidConnectCode(code) {
		return PairingResult{}, errors.New("invalid connect code")
	}
	body, err := json.Marshal(map[string]string{"connectCode": strings.ToUpper(code), "deviceName": c.DeviceName})
	if err != nil {
		return PairingResult{}, errors.New("could not prepare Hub onboarding")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint("/v1/device-onboarding-codes/claim"), bytes.NewReader(body))
	if err != nil {
		return PairingResult{}, errors.New("could not prepare Hub onboarding")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return PairingResult{}, errors.New("Hub could not receive the Local pairing request")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		problem := readProblem(response)
		if response.StatusCode == http.StatusNotFound {
			if problem.Code == "connect_code_invalid" {
				return PairingResult{}, ErrConnectCodeInvalid
			}
			return PairingResult{}, ErrConnectUnsupported
		}
		return PairingResult{}, fmt.Errorf("Hub could not start pairing: %s", problem.Code)
	}
	var authorization onboardingClaimResponse
	if err := decodeBounded(response.Body, &authorization); err != nil || !onboardingUUID.MatchString(authorization.ID) ||
		authorization.DeviceCode == "" || len(authorization.DeviceCode) > 256 ||
		!authorization.ExpiresAt.After(time.Now()) || authorization.IntervalSeconds < 1 || authorization.IntervalSeconds > 30 {
		return PairingResult{}, errors.New("Hub returned an invalid onboarding response")
	}
	c.pairingMu.Lock()
	if c.pairings == nil {
		c.pairings = make(map[string]pairingSecret)
	}
	c.pairings[authorization.ID] = pairingSecret{deviceCode: authorization.DeviceCode, expiresAt: authorization.ExpiresAt}
	c.pairingMu.Unlock()
	return PairingResult{State: "pending", PairingID: authorization.ID,
		ExpiresAt: authorization.ExpiresAt, IntervalSeconds: authorization.IntervalSeconds}, nil
}

type checkInRequest struct {
	ClientVersion        string       `json:"clientVersion"`
	Capabilities         []string     `json:"capabilities"`
	OS                   string       `json:"os"`
	InstallChannel       string       `json:"installChannel"`
	AppliedConfigVersion int64        `json:"appliedConfigVersion"`
	AgentsFound          []string     `json:"agentsFound"`
	Queue                checkInQueue `json:"queue"`
}

type checkInQueue struct {
	Pending   int `json:"pending"`
	Failing   int `json:"failing"`
	FirstSync struct {
		RecentDone   int    `json:"recentDone"`
		RecentTotal  int    `json:"recentTotal"`
		HistoryState string `json:"historyState"`
	} `json:"firstSync"`
}

type checkInResponse struct {
	NextCheckInSeconds int `json:"nextCheckInSeconds"`
}

func (c *Client) CheckIn(ctx context.Context, version string) (time.Duration, error) {
	if !c.configured() {
		return 0, errors.New("Hub server is not configured")
	}
	credential, err := c.Credentials.Load(ctx)
	if err != nil {
		return 0, ErrCredentialStoreUnavailable
	}
	if !clientVersion.MatchString(version) {
		version = "0.0.0"
	}
	input := checkInRequest{ClientVersion: version, Capabilities: localCapabilities(), OS: runtime.GOOS,
		InstallChannel: normalizedInstallChannel(c.InstallChannel),
		AgentsFound:    []string{}, Queue: checkInQueue{}}
	input.Queue.FirstSync.HistoryState = "not_started"
	body, err := json.Marshal(input)
	if err != nil {
		return 0, errors.New("could not prepare Hub check-in")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/v4/devices/me/check-in"), bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("could not prepare Hub check-in")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Device "+credential)
	response, err := c.httpClient().Do(request)
	if err != nil {
		return 0, errors.New("Local could not reach Hub for its first check-in")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		problem := readProblem(response)
		if problem.Code == "device_revoked" {
			revoked := V4Problem{Code: problem.Code, HTTPStatus: response.StatusCode}
			cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancelCleanup()
			var deleteErr error
			if deleter, ok := c.Credentials.(ConditionalCredentialDeleter); ok {
				_, deleteErr = deleter.DeleteIfMatches(cleanupCtx, credential)
			} else {
				deleteErr = c.Credentials.Delete(cleanupCtx)
			}
			if deleteErr != nil {
				return 0, errors.Join(revoked, fmt.Errorf("delete revoked Hub credential: %w", deleteErr))
			}
			return 0, revoked
		}
		return 0, fmt.Errorf("Hub check-in failed: %s", problem.Code)
	}
	var result checkInResponse
	if err := decodeBounded(response.Body, &result); err != nil || result.NextCheckInSeconds < 1 || result.NextCheckInSeconds > 3600 {
		return 0, errors.New("Hub returned an invalid check-in response")
	}
	return time.Duration(result.NextCheckInSeconds) * time.Second, nil
}
