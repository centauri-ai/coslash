package hubclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	fullsessionv1 "github.com/centauri-ai/coslash/collector/fullsession/v1"
	"github.com/centauri-ai/coslash/collector/internal/fullsessionexport"
)

type FullSessionLoader func(sourceID, agent, sessionID, revisionID string) (*fullsessionv1.Record, fullsessionexport.Repository, error)

type Client struct {
	BaseURL          *url.URL
	HTTP             *http.Client
	Credentials      CredentialStore
	DeviceName       string
	InstallID        string
	MachineID        string
	CollectorVersion string
	InstallChannel   string
	LoadFullSession  FullSessionLoader

	pairingMu sync.Mutex
	pairings  map[string]pairingSecret
}

type pairingSecret struct {
	deviceCode   string
	expiresAt    time.Time
	credential   string
	saveFailures int
}

const maxPairingCredentialSaveAttempts = 3

func (c *Client) configured() bool {
	return c != nil && c.BaseURL != nil && c.Credentials != nil
}

func (c *Client) httpClient() *http.Client {
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copy := *client
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &copy
}

func (c *Client) endpoint(path string) string {
	endpoint := *c.BaseURL
	path, query, _ := strings.Cut(path, "?")
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.TrimLeft(path, "/")
	endpoint.RawPath = ""
	endpoint.RawQuery = query
	endpoint.Fragment = ""
	return endpoint.String()
}

func (c *Client) Destination(ctx context.Context) (DestinationResult, error) {
	if !c.configured() {
		return DestinationResult{ContractVersion: ContractVersion, State: "signed_out", Configured: false}, nil
	}
	credential, err := c.Credentials.Load(ctx)
	if errors.Is(err, ErrNotPaired) {
		return DestinationResult{ContractVersion: ContractVersion, State: "pairing_required", Configured: true, HubURL: c.BaseURL.String()}, nil
	}
	if err != nil {
		return DestinationResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/v1/share-destination"), nil)
	if err != nil {
		return DestinationResult{}, err
	}
	request.Header.Set("Authorization", "Device "+credential)
	response, err := c.httpClient().Do(request)
	if err != nil {
		return DestinationResult{}, fmt.Errorf("load Hub destination: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return DestinationResult{}, remoteError(response)
	}
	var result DestinationResult
	if err := decodeBounded(response.Body, &result); err != nil {
		return DestinationResult{}, fmt.Errorf("decode Hub destination: %w", err)
	}
	if result.ContractVersion != ContractVersion || result.State == "ready" && result.Destination == nil {
		return DestinationResult{}, errors.New("decode Hub destination: contract mismatch")
	}
	result.Configured = true
	result.HubURL = c.BaseURL.String()
	return result, nil
}

type authorizationResponse struct {
	ID                      string    `json:"id"`
	DeviceCode              string    `json:"deviceCode"`
	UserCode                string    `json:"userCode"`
	VerificationURI         string    `json:"verificationUri"`
	VerificationURIComplete string    `json:"verificationUriComplete"`
	ExpiresAt               time.Time `json:"expiresAt"`
	IntervalSeconds         int       `json:"intervalSeconds"`
}

func (c *Client) BeginPairing(ctx context.Context) (PairingResult, error) {
	if !c.configured() {
		return PairingResult{}, errors.New("Hub server is not configured")
	}
	fields := map[string]string{"deviceName": c.DeviceName}
	c.addPairIdentity(fields)
	response, err := c.postPairingJSON(ctx, "/v1/device-authorizations", fields, "begin Hub pairing")
	if err != nil {
		return PairingResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return PairingResult{}, remoteError(response)
	}
	var authorization authorizationResponse
	if err := decodeBounded(response.Body, &authorization); err != nil {
		return PairingResult{}, fmt.Errorf("decode Hub pairing: %w", err)
	}
	if authorization.ID == "" || authorization.DeviceCode == "" || authorization.UserCode == "" ||
		time.Now().After(authorization.ExpiresAt) ||
		!c.validVerificationURL(authorization.VerificationURI, authorization.DeviceCode) ||
		authorization.VerificationURIComplete != "" &&
			!c.validVerificationURL(authorization.VerificationURIComplete, authorization.DeviceCode) {
		return PairingResult{}, errors.New("decode Hub pairing: incomplete authorization")
	}
	c.pairingMu.Lock()
	if c.pairings == nil {
		c.pairings = make(map[string]pairingSecret)
	}
	c.pairings[authorization.ID] = pairingSecret{deviceCode: authorization.DeviceCode, expiresAt: authorization.ExpiresAt}
	c.pairingMu.Unlock()
	return PairingResult{
		State: "pending", PairingID: authorization.ID, UserCode: authorization.UserCode,
		VerificationURI: authorization.VerificationURI, VerificationURIComplete: authorization.VerificationURIComplete,
		ExpiresAt: authorization.ExpiresAt, IntervalSeconds: authorization.IntervalSeconds,
	}, nil
}

func (c *Client) PollPairing(ctx context.Context, pairingID string) (PairingResult, error) {
	c.pairingMu.Lock()
	secret, ok := c.pairings[pairingID]
	c.pairingMu.Unlock()
	if !ok {
		return PairingResult{State: PairingStateExpired}, nil
	}
	if secret.credential != "" {
		return c.savePairingCredential(ctx, pairingID, secret.credential)
	}
	if time.Now().After(secret.expiresAt) {
		c.forgetPairing(pairingID)
		return PairingResult{State: "expired"}, nil
	}
	fields := map[string]string{"deviceCode": secret.deviceCode}
	c.addPairIdentity(fields)
	response, err := c.postJSON(ctx, "/v1/device-authorizations/token", fields)
	if err != nil {
		return PairingResult{}, fmt.Errorf("poll Hub pairing: %w", err)
	}
	if response.StatusCode == http.StatusBadRequest && (fields["installId"] != "" || fields["machineId"] != "") {
		problem := readProblem(response)
		_ = response.Body.Close()
		if problem.Code == "invalid_request" {
			delete(fields, "installId")
			delete(fields, "machineId")
			response, err = c.postJSON(ctx, "/v1/device-authorizations/token", fields)
			if err != nil {
				return PairingResult{}, fmt.Errorf("poll Hub pairing: %w", err)
			}
		} else {
			return c.pairingPollResult(pairingID, response.StatusCode, problem)
		}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return c.pairingPollResult(pairingID, response.StatusCode, readProblem(response))
	}
	var token struct {
		DeviceID   string `json:"deviceId"`
		Credential string `json:"credential"`
		TokenType  string `json:"tokenType"`
		Scope      string `json:"scope"`
	}
	if err := decodeBounded(response.Body, &token); err != nil || token.DeviceID == "" ||
		token.Credential == "" || token.TokenType != "Device" || token.Scope != "ingest" {
		return PairingResult{}, errors.New("decode Hub credential: invalid response")
	}
	c.pairingMu.Lock()
	secret = c.pairings[pairingID]
	if secret.deviceCode == "" {
		c.pairingMu.Unlock()
		return PairingResult{State: PairingStateExpired}, nil
	}
	secret.credential = token.Credential
	c.pairings[pairingID] = secret
	c.pairingMu.Unlock()
	return c.savePairingCredential(ctx, pairingID, token.Credential)
}

func (c *Client) pairingPollResult(pairingID string, status int, problem Problem) (PairingResult, error) {
	if problem.Code == "pairing_pending" {
		return PairingResult{State: "pending", PairingID: pairingID}, nil
	}
	if problem.Code == "onboarding_declined" {
		c.forgetPairing(pairingID)
		return PairingResult{State: "declined"}, nil
	}
	if problem.Code == "pairing_expired" || status == http.StatusGone {
		c.forgetPairing(pairingID)
		return PairingResult{State: "expired"}, nil
	}
	return PairingResult{}, problem
}

func (c *Client) savePairingCredential(ctx context.Context, pairingID, credential string) (PairingResult, error) {
	saveContext, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelSave()
	if c.Credentials == nil || c.Credentials.Save(saveContext, credential) != nil {
		c.pairingMu.Lock()
		secret, ok := c.pairings[pairingID]
		if ok {
			secret.saveFailures++
			c.pairings[pairingID] = secret
		}
		attempts := secret.saveFailures
		c.pairingMu.Unlock()
		if !ok {
			return PairingResult{State: PairingStateRetrying}, nil
		}
		if attempts >= maxPairingCredentialSaveAttempts {
			c.forgetPairing(pairingID)
			return PairingResult{State: PairingStateCredentialStoreFailed}, nil
		}
		return PairingResult{State: PairingStateRetrying, PairingID: pairingID}, nil
	}
	c.forgetPairing(pairingID)
	return PairingResult{State: PairingStatePaired}, nil
}

func (c *Client) forgetPairing(id string) {
	c.pairingMu.Lock()
	delete(c.pairings, id)
	c.pairingMu.Unlock()
}

func (c *Client) validVerificationURL(raw, forbidden string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.User != nil ||
		parsed.Scheme != c.BaseURL.Scheme || parsed.Host != c.BaseURL.Host {
		return false
	}
	decoded, _ := url.QueryUnescape(raw)
	return forbidden == "" || !strings.Contains(raw, forbidden) && !strings.Contains(decoded, forbidden)
}

type Problem struct {
	Code              string `json:"code"`
	Detail            string `json:"detail"`
	Status            int    `json:"status"`
	RetryAfterSeconds *int   `json:"-"`
}

func (p Problem) Error() string {
	if p.Detail != "" {
		return p.Detail
	}
	return "Hub request failed"
}

func remoteError(response *http.Response) error { return readProblem(response) }

func readProblem(response *http.Response) Problem {
	problem := Problem{Status: response.StatusCode}
	_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&problem)
	if retryAfter, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && retryAfter >= 0 {
		problem.RetryAfterSeconds = &retryAfter
	}
	if problem.Code == "" {
		problem.Code = "share_failed"
	}
	return problem
}

func decodeBounded(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}
