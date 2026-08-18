package hubclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionexport"
	"github.com/centauri-ai/coslash/collector/internal/sessionpreview"
)

const snapshotMediaType = "application/vnd.coslash.session-snapshot.v1+json"

type SessionLoader func(string, int64) (*session.Session, error)

type Client struct {
	BaseURL          *url.URL
	HTTP             *http.Client
	Credentials      CredentialStore
	DeviceName       string
	CollectorVersion string
	LoadSession      SessionLoader

	pairingMu sync.Mutex
	pairings  map[string]pairingSecret
}

type pairingSecret struct {
	deviceCode string
	expiresAt  time.Time
}

func (c *Client) configured() bool {
	return c != nil && c.BaseURL != nil && c.Credentials != nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) endpoint(path string) string {
	return c.BaseURL.ResolveReference(&url.URL{Path: path}).String()
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
	body, _ := json.Marshal(map[string]string{"deviceName": c.DeviceName})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/v1/device-authorizations"), bytes.NewReader(body))
	if err != nil {
		return PairingResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return PairingResult{}, fmt.Errorf("begin Hub pairing: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return PairingResult{}, remoteError(response)
	}
	var authorization authorizationResponse
	if err := decodeBounded(response.Body, &authorization); err != nil {
		return PairingResult{}, fmt.Errorf("decode Hub pairing: %w", err)
	}
	if authorization.ID == "" || authorization.DeviceCode == "" || authorization.UserCode == "" {
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
	if !ok || time.Now().After(secret.expiresAt) {
		return PairingResult{State: "expired"}, nil
	}
	body, _ := json.Marshal(map[string]string{"deviceCode": secret.deviceCode})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/v1/device-authorizations/token"), bytes.NewReader(body))
	if err != nil {
		return PairingResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return PairingResult{}, fmt.Errorf("poll Hub pairing: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		problem := readProblem(response)
		if problem.Code == "pairing_pending" {
			return PairingResult{State: "pending", PairingID: pairingID}, nil
		}
		if problem.Code == "pairing_expired" || response.StatusCode == http.StatusGone {
			c.forgetPairing(pairingID)
			return PairingResult{State: "expired"}, nil
		}
		return PairingResult{}, problem
	}
	var token struct {
		DeviceID   string `json:"deviceId"`
		Credential string `json:"credential"`
		TokenType  string `json:"tokenType"`
		Scope      string `json:"scope"`
	}
	if err := decodeBounded(response.Body, &token); err != nil || token.Credential == "" {
		return PairingResult{}, errors.New("decode Hub credential: invalid response")
	}
	if err := c.Credentials.Save(ctx, token.Credential); err != nil {
		return PairingResult{}, err
	}
	c.forgetPairing(pairingID)
	return PairingResult{State: "paired"}, nil
}

func (c *Client) forgetPairing(id string) {
	c.pairingMu.Lock()
	delete(c.pairings, id)
	c.pairingMu.Unlock()
}

type uploadResponse struct {
	SessionID          string    `json:"sessionId"`
	RevisionID         string    `json:"revisionId"`
	RepositoryID       string    `json:"repositoryId"`
	CanonicalWeekStart string    `json:"canonicalWeekStart"`
	Deduplicated       bool      `json:"deduplicated"`
	SharedAt           time.Time `json:"sharedAt"`
	BriefStatus        string    `json:"briefStatus"`
	TeamURL            string    `json:"teamUrl,omitempty"`
}

func (c *Client) Share(ctx context.Context, input ShareRequest) (ShareResult, error) {
	if !c.configured() || c.LoadSession == nil {
		return ShareResult{}, errors.New("Hub sharing is not configured")
	}
	if input.ContractVersion != ContractVersion || strings.TrimSpace(input.RequestID) == "" || len(input.Items) == 0 || len(input.Items) > 100 {
		return ShareResult{}, errors.New("invalid hub-share/v1 request")
	}
	credential, err := c.Credentials.Load(ctx)
	if err != nil {
		return ShareResult{}, err
	}
	result := ShareResult{ContractVersion: ContractVersion, RequestID: input.RequestID, Results: make([]ShareItemResult, 0, len(input.Items))}
	accepted := 0
	for _, item := range input.Items {
		itemResult := c.shareItem(ctx, credential, item)
		if itemResult.State != "failed" {
			accepted++
		}
		result.Results = append(result.Results, itemResult)
	}
	switch {
	case accepted == len(input.Items):
		result.State = "succeeded"
	case accepted == 0:
		result.State = "failed"
	default:
		result.State = "partial"
	}
	return result, nil
}

func (c *Client) shareItem(ctx context.Context, credential string, item ShareItemRequest) ShareItemResult {
	failed := func(code string, retryable bool) ShareItemResult {
		return ShareItemResult{LocalSessionID: item.LocalSessionID, IdempotencyKey: item.IdempotencyKey,
			State: "failed", Error: &ItemError{Code: code, Retryable: retryable}}
	}
	if item.Consent.PreviewContractVersion != PreviewVersion || item.Consent.SourceRevision <= 0 ||
		item.Consent.PayloadBytes <= 0 || item.Consent.DestinationWorkspaceID == "" ||
		len(item.IdempotencyKey) < 16 || len(item.IdempotencyKey) > 200 {
		return failed("invalid_share_request", false)
	}
	separator := strings.IndexByte(item.LocalSessionID, ':')
	if separator <= 0 || separator == len(item.LocalSessionID)-1 {
		return failed("invalid_share_request", false)
	}
	agent, sessionID := item.LocalSessionID[:separator], item.LocalSessionID[separator+1:]
	found, err := c.LoadSession(sessionID, item.Consent.SourceRevision)
	if err != nil {
		return failed("share_failed", true)
	}
	if found == nil || found.Agent != agent || found.LastActivityTime != item.Consent.SourceRevision {
		return failed("consent_stale", true)
	}
	preview := sessionpreview.Build(*found, sessionexport.BuildOptions{
		CollectorVersion: c.CollectorVersion,
		RepositoryRoot:   session.RepositoryRoot(found.WorkingDirectory),
	}, item.Consent.SourceRevision)
	payload, err := sessionpreview.UploadBytes(preview)
	if err != nil || preview.Snapshot == nil || strings.TrimPrefix(preview.Snapshot.ContentHash, "sha256:") != item.Consent.ContentHash || len(payload) != item.Consent.PayloadBytes {
		return failed("consent_stale", true)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil || writer.Close() != nil {
		return failed("share_failed", true)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/v1/session-revisions"), &compressed)
	if err != nil {
		return failed("share_failed", true)
	}
	request.Header.Set("Authorization", "Device "+credential)
	request.Header.Set("Content-Type", snapshotMediaType)
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("Idempotency-Key", item.IdempotencyKey)
	request.Header.Set("Coslash-Destination-Workspace-Id", item.Consent.DestinationWorkspaceID)
	response, err := c.httpClient().Do(request)
	if err != nil {
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			return failed("timeout", true)
		}
		return failed("network_unavailable", true)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		problem := readProblem(response)
		result := failed(mapProblem(problem.Code), retryable(problem.Code))
		result.Error.RetryAfterSeconds = problem.RetryAfterSeconds
		return result
	}
	var upload uploadResponse
	if err := decodeBounded(response.Body, &upload); err != nil || upload.SessionID == "" || upload.RevisionID == "" || upload.RepositoryID == "" || upload.CanonicalWeekStart == "" {
		return failed("share_failed", true)
	}
	state := "accepted"
	if upload.Deduplicated {
		state = "already_accepted"
	}
	path := "/repos/" + url.PathEscape(upload.RepositoryID) + "/sessions/" + url.PathEscape(upload.CanonicalWeekStart)
	return ShareItemResult{
		LocalSessionID: item.LocalSessionID, IdempotencyKey: item.IdempotencyKey, State: state,
		SessionID: upload.SessionID, RevisionID: upload.RevisionID, Deduplicated: upload.Deduplicated,
		SharedAt: &upload.SharedAt, BriefState: upload.BriefStatus,
		Route: &RouteHandoff{HubContractVersion: "hub-read/v1", RepositoryID: upload.RepositoryID, CanonicalWeekStart: upload.CanonicalWeekStart, Path: path},
	}
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

func mapProblem(code string) string {
	switch code {
	case "unsupported_snapshot", "unsupported_snapshot_version":
		return "unsupported_snapshot_version"
	case "device_dormant":
		return "credential_dormant"
	case "device_revoked":
		return "credential_revoked"
	case "consent_stale", "destination_changed", "idempotency_conflict", "rate_limited", "snapshot_invalid", "snapshot_too_large", "invalid_share_request", "timeout", "temporary_unavailable":
		return code
	case "unauthorized":
		return "unauthorized"
	default:
		return "share_failed"
	}
}

func retryable(code string) bool {
	switch mapProblem(code) {
	case "unauthorized", "credential_dormant", "consent_stale", "destination_changed", "rate_limited", "network_unavailable", "timeout", "temporary_unavailable", "share_failed":
		return true
	default:
		return false
	}
}
