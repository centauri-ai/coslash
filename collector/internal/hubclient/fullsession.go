package hubclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/fullsessionexport"
)

const fullSessionTimeout = 60 * time.Second

type FullSessionSelection struct {
	SourceID   string `json:"sourceId"`
	Agent      string `json:"agent"`
	SessionID  string `json:"sessionId"`
	RevisionID string `json:"revisionId"`
}

type FullSessionPreviewProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Action  string `json:"action"`
}

type FullSessionPreview struct {
	AdapterVersion     string                      `json:"adapterVersion"`
	State              string                      `json:"state"`
	ApprovalAllowed    bool                        `json:"approvalAllowed"`
	Selection          FullSessionSelection        `json:"selection"`
	SchemaVersion      string                      `json:"schemaVersion,omitempty"`
	MediaType          string                      `json:"mediaType,omitempty"`
	RecordBytes        int                         `json:"recordBytes,omitempty"`
	PayloadBytes       int                         `json:"payloadBytes,omitempty"`
	MaxRecordBytes     int64                       `json:"maxRecordBytes,omitempty"`
	RecordSHA256       string                      `json:"recordSha256,omitempty"`
	AudienceVersion    string                      `json:"audienceVersion,omitempty"`
	EmbeddedSecretRisk bool                        `json:"embeddedSecretRisk"`
	Envelope           *fullsessionexport.Envelope `json:"envelope,omitempty"`
	Problem            *FullSessionPreviewProblem  `json:"problem,omitempty"`
}

type fullSessionCapabilities struct {
	Product             string   `json:"product"`
	ServerID            string   `json:"serverId"`
	DisplayName         string   `json:"displayName"`
	ProtocolVersions    []string `json:"protocolVersions"`
	SnapshotVersions    []string `json:"snapshotVersions"`
	MaxSnapshotBytes    int64    `json:"maxSnapshotBytes"`
	FullSessionVersions []string `json:"fullSessionVersions"`
	MaxFullSessionBytes int64    `json:"maxFullSessionBytes"`
	MaxRequestBytes     int64    `json:"maxRequestBytes"`
	PairingURL          string   `json:"pairingUrl"`
	TeamURL             string   `json:"teamUrl"`
}

type fullSessionCapabilityError struct {
	code      string
	retryable bool
	cause     error
}

func (failure *fullSessionCapabilityError) Error() string {
	return failure.cause.Error()
}

func capabilityFailure(code string, retryable bool, cause error) error {
	return &fullSessionCapabilityError{code: code, retryable: retryable, cause: cause}
}

func classifyCapabilityFailure(err error) (string, bool) {
	var failure *fullSessionCapabilityError
	if errors.As(err, &failure) {
		return failure.code, failure.retryable
	}
	return "temporary_unavailable", true
}

func (c *Client) fullSessionCapabilities(ctx context.Context) (fullSessionCapabilities, error) {
	if c == nil || c.BaseURL == nil {
		return fullSessionCapabilities{}, capabilityFailure("incompatible_server", false, errors.New("Hub server is not configured"))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/.well-known/coslash-server"), nil)
	if err != nil {
		return fullSessionCapabilities{}, capabilityFailure("temporary_unavailable", true, err)
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return fullSessionCapabilities{}, capabilityFailure("network_unavailable", true, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := fmt.Errorf("Hub capability request returned %d", response.StatusCode)
		if response.StatusCode >= http.StatusInternalServerError || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests {
			return fullSessionCapabilities{}, capabilityFailure("temporary_unavailable", true, failure)
		}
		return fullSessionCapabilities{}, capabilityFailure("incompatible_server", false, failure)
	}
	var capability fullSessionCapabilities
	if err := decodeBounded(response.Body, &capability); err != nil {
		return fullSessionCapabilities{}, capabilityFailure("temporary_unavailable", true, err)
	}
	if capability.Product != "coslash-server" || !slices.Contains(capability.ProtocolVersions, "v2") ||
		!slices.Contains(capability.FullSessionVersions, "full-session-record/v1") ||
		capability.MaxFullSessionBytes <= 0 || capability.MaxRequestBytes <= 0 {
		return fullSessionCapabilities{}, capabilityFailure("incompatible_server", false, errors.New("Hub does not advertise compatible full-session v2 support"))
	}
	return capability, nil
}

func previewProblem(selection FullSessionSelection, state, code, message, action string) FullSessionPreview {
	return FullSessionPreview{
		AdapterVersion: fullsessionexport.PreviewVersion, State: state, Selection: selection,
		Problem: &FullSessionPreviewProblem{Code: code, Message: message, Action: action},
	}
}

// PreviewFullSession verifies the paired destination and capability before
// content so a preflight failure never loads or returns full session data.
func (c *Client) PreviewFullSession(ctx context.Context, selection FullSessionSelection) (FullSessionPreview, error) {
	if c == nil || !c.configured() || c.LoadFullSession == nil {
		return previewProblem(selection, "invalid", "invalid_selection", "This full-session selection is invalid.", "Select the session again."), errors.New("full-session client is not configured")
	}
	if selection.SourceID == "" || selection.Agent == "" || selection.SessionID == "" || selection.RevisionID == "" {
		return previewProblem(selection, "invalid", "invalid_selection", "This full-session selection is invalid.", "Select the session again."), nil
	}
	destination, err := c.Destination(ctx)
	if err != nil {
		return previewProblem(selection, "unavailable", "temporary_unavailable", "The paired Hub destination could not be verified.", "Retry without changing the selected revision."), fmt.Errorf("verify full-session destination: %w", err)
	}
	if destination.State != "ready" || destination.Destination == nil || destination.Destination.CredentialState != "paired" {
		return previewProblem(selection, "unavailable", "unauthorized", "A paired Hub destination is required before loading the complete revision.", "Pair this device and select a ready destination, then preview again."), nil
	}
	if !validAudienceVersion(destination.Destination.AudienceVersion) {
		return previewProblem(selection, "incompatible_server", "incompatible_server", "This Hub cannot bind approval to the reviewed audience.", "Update or reconfigure the Hub, then preview again."), errors.New("Hub destination omitted a valid audience version")
	}
	capability, err := c.fullSessionCapabilities(ctx)
	if err != nil {
		code, retryable := classifyCapabilityFailure(err)
		if retryable {
			message := "The Hub capability check is temporarily unavailable."
			if code == "network_unavailable" {
				message = "The Hub could not be reached for its capability check."
			}
			return previewProblem(selection, "unavailable", code, message, "Retry without changing the selected revision."), fmt.Errorf("load full-session capabilities: %w", err)
		}
		return previewProblem(selection, "incompatible_server", code, "This Hub does not currently accept full-session v2 revisions.", "Update or reconfigure the Hub, then preview again."), fmt.Errorf("load full-session capabilities: %w", err)
	}
	record, repository, err := c.LoadFullSession(selection.SourceID, selection.Agent, selection.SessionID, selection.RevisionID)
	if err != nil {
		return previewProblem(selection, "stale_source", "stale_source", "The selected full revision is no longer available.", "Refresh sessions and review the current revision."), fmt.Errorf("load full-session record: %w", err)
	}
	if record == nil || record.SourceID != selection.SourceID || record.Agent != selection.Agent ||
		record.SessionID != selection.SessionID || record.RevisionID != selection.RevisionID {
		return previewProblem(selection, "stale_source", "stale_source", "The selected full revision is no longer available.", "Refresh sessions and review the current revision."), nil
	}
	payload, recordBytes, hash, err := fullsessionexport.Marshal(*record, repository)
	if err != nil {
		return previewProblem(selection, "invalid", "invalid_record", "The selected full revision cannot be serialized safely.", "Refresh or repair the source before sharing."), fmt.Errorf("serialize full-session record: %w", err)
	}
	if int64(len(recordBytes)) > capability.MaxFullSessionBytes {
		preview := previewProblem(selection, "oversized", "full_session_too_large", "The full revision exceeds this Hub's upload limit.", "Choose a smaller revision or ask the Hub operator to raise the bounded limit.")
		preview.RecordBytes = len(recordBytes)
		preview.MaxRecordBytes = capability.MaxFullSessionBytes
		return preview, nil
	}
	compressed, err := compressFullSession(payload)
	if err != nil {
		return previewProblem(selection, "unavailable", "temporary_unavailable", "The full revision could not be prepared for upload.", "Retry without changing the selected revision."), fmt.Errorf("compress full-session preview: %w", err)
	}
	if int64(len(compressed)) > capability.MaxRequestBytes {
		return previewProblem(selection, "oversized", "full_session_too_large", "The full revision exceeds this Hub's request limit.", "Choose a smaller revision or ask the Hub operator to raise the bounded limit."), nil
	}
	return FullSessionPreview{
		AdapterVersion: fullsessionexport.PreviewVersion, State: "ready", ApprovalAllowed: true,
		Selection: selection, SchemaVersion: fullsessionexport.SchemaVersion, MediaType: fullsessionexport.MediaType,
		RecordBytes: len(recordBytes), PayloadBytes: len(payload), MaxRecordBytes: capability.MaxFullSessionBytes,
		RecordSHA256: hash, AudienceVersion: destination.Destination.AudienceVersion, EmbeddedSecretRisk: true,
		Envelope: &fullsessionexport.Envelope{
			SchemaVersion: fullsessionexport.SchemaVersion, MediaType: fullsessionexport.MediaType,
			RecordByteCount: len(recordBytes), RecordSHA256: hash, Repository: repository, Record: *record,
		},
	}, nil
}

type FullSessionConsent struct {
	PreviewContractVersion string                       `json:"previewContractVersion"`
	RevisionID             string                       `json:"revisionId"`
	RecordSHA256           string                       `json:"recordSha256"`
	RecordBytes            int                          `json:"recordBytes"`
	PayloadBytes           int                          `json:"payloadBytes"`
	Repository             fullsessionexport.Repository `json:"repository"`
	DestinationWorkspaceID string                       `json:"destinationWorkspaceId"`
	DestinationName        string                       `json:"destinationName"`
	AudienceMemberCount    int                          `json:"audienceMemberCount"`
	AudienceVersion        string                       `json:"audienceVersion"`
}

type FullSessionShareRequest struct {
	ContractVersion string               `json:"contractVersion"`
	Selection       FullSessionSelection `json:"selection"`
	IdempotencyKey  string               `json:"idempotencyKey"`
	Consent         FullSessionConsent   `json:"consent"`
}

type FullSessionRoute struct {
	HubContractVersion string `json:"hubContractVersion"`
	Path               string `json:"path"`
}

type FullSessionShareError struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

type FullSessionShareResult struct {
	ContractVersion string                 `json:"contractVersion"`
	State           string                 `json:"state"`
	Selection       FullSessionSelection   `json:"selection"`
	IdempotencyKey  string                 `json:"idempotencyKey"`
	RepositoryID    string                 `json:"repositoryId,omitempty"`
	ByteCount       int                    `json:"byteCount,omitempty"`
	ContentSHA256   string                 `json:"contentSha256,omitempty"`
	Deduplicated    bool                   `json:"deduplicated"`
	SharedAt        *time.Time             `json:"sharedAt,omitempty"`
	Route           *FullSessionRoute      `json:"route,omitempty"`
	Error           *FullSessionShareError `json:"error,omitempty"`
}

type fullSessionUploadResponse struct {
	SourceID      string    `json:"sourceId"`
	Agent         string    `json:"agent"`
	SessionID     string    `json:"sessionId"`
	RevisionID    string    `json:"revisionId"`
	RepositoryID  string    `json:"repositoryId"`
	ByteCount     int       `json:"byteCount"`
	ContentSHA256 string    `json:"contentSha256"`
	Deduplicated  bool      `json:"deduplicated"`
	SharedAt      time.Time `json:"sharedAt"`
	RevisionURL   string    `json:"revisionUrl"`
}

func fullSessionFailed(request FullSessionShareRequest, code string, retryable bool) FullSessionShareResult {
	return FullSessionShareResult{
		ContractVersion: fullsessionexport.ShareVersion, State: "failed", Selection: request.Selection,
		IdempotencyKey: request.IdempotencyKey, Error: &FullSessionShareError{Code: code, Retryable: retryable},
	}
}

func validFullSessionRequest(request FullSessionShareRequest) bool {
	consent := request.Consent
	return request.ContractVersion == fullsessionexport.ShareVersion && request.Selection.SourceID != "" &&
		request.Selection.Agent != "" && request.Selection.SessionID != "" && request.Selection.RevisionID != "" &&
		len(request.IdempotencyKey) >= 16 && len(request.IdempotencyKey) <= 200 &&
		consent.PreviewContractVersion == fullsessionexport.PreviewVersion && consent.RevisionID == request.Selection.RevisionID &&
		strings.HasPrefix(consent.RecordSHA256, "sha256:") && len(consent.RecordSHA256) == 71 &&
		consent.RecordBytes > 0 && consent.PayloadBytes > 0 && consent.Repository.Canonical != "" &&
		consent.DestinationWorkspaceID != "" && consent.DestinationName != "" && consent.AudienceMemberCount >= 0 &&
		validAudienceVersion(consent.AudienceVersion)
}

func validAudienceVersion(value string) bool {
	return value != "" && len(value) <= 200 && !strings.ContainsAny(value, "\r\n")
}

func (c *Client) ShareFullSession(ctx context.Context, request FullSessionShareRequest) (FullSessionShareResult, error) {
	if c == nil || !c.configured() || c.LoadFullSession == nil {
		return fullSessionFailed(request, "invalid_full_session_request", false), errors.New("full-session client is not configured")
	}
	if !validFullSessionRequest(request) {
		return fullSessionFailed(request, "invalid_full_session_request", false), nil
	}
	credential, err := c.Credentials.Load(ctx)
	if errors.Is(err, ErrNotPaired) {
		return fullSessionFailed(request, "unauthorized", true), nil
	}
	if err != nil {
		return fullSessionFailed(request, "temporary_unavailable", true), fmt.Errorf("load full-session credential: %w", err)
	}
	destination, err := c.Destination(ctx)
	if err != nil {
		return fullSessionFailed(request, "temporary_unavailable", true), fmt.Errorf("verify full-session destination: %w", err)
	}
	if destination.State != "ready" || destination.Destination == nil ||
		destination.Destination.WorkspaceID != request.Consent.DestinationWorkspaceID ||
		destination.Destination.WorkspaceName != request.Consent.DestinationName ||
		destination.Destination.CurrentMemberCount != request.Consent.AudienceMemberCount ||
		destination.Destination.AudienceVersion != request.Consent.AudienceVersion {
		return fullSessionFailed(request, "review_binding_changed", true), nil
	}
	capability, err := c.fullSessionCapabilities(ctx)
	if err != nil {
		code, retryable := classifyCapabilityFailure(err)
		return fullSessionFailed(request, code, retryable), fmt.Errorf("load full-session capabilities: %w", err)
	}
	record, repository, err := c.LoadFullSession(request.Selection.SourceID, request.Selection.Agent,
		request.Selection.SessionID, request.Selection.RevisionID)
	if err != nil {
		return fullSessionFailed(request, "review_binding_changed", true), fmt.Errorf("load full-session record: %w", err)
	}
	if record == nil || record.SourceID != request.Selection.SourceID ||
		record.Agent != request.Selection.Agent || record.SessionID != request.Selection.SessionID ||
		record.RevisionID != request.Selection.RevisionID {
		return fullSessionFailed(request, "review_binding_changed", true), nil
	}
	payload, recordBytes, hash, err := fullsessionexport.Marshal(*record, repository)
	if err != nil {
		return fullSessionFailed(request, "full_session_invalid", false), fmt.Errorf("serialize full-session record: %w", err)
	}
	if int64(len(recordBytes)) > capability.MaxFullSessionBytes {
		return fullSessionFailed(request, "full_session_too_large", false), nil
	}
	if request.Consent.RecordSHA256 != hash || request.Consent.RecordBytes != len(recordBytes) ||
		request.Consent.PayloadBytes != len(payload) || request.Consent.Repository != repository {
		return fullSessionFailed(request, "review_binding_changed", true), nil
	}
	compressed, err := compressFullSession(payload)
	if err != nil {
		return fullSessionFailed(request, "temporary_unavailable", true), fmt.Errorf("compress full-session upload: %w", err)
	}
	if int64(len(compressed)) > capability.MaxRequestBytes {
		return fullSessionFailed(request, "full_session_too_large", false), nil
	}
	shareCtx, cancel := context.WithTimeout(ctx, fullSessionTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(shareCtx, http.MethodPost, c.endpoint("/v2/session-revisions"), bytes.NewReader(compressed))
	if err != nil {
		return fullSessionFailed(request, "temporary_unavailable", true), fmt.Errorf("build full-session upload request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Device "+credential)
	httpRequest.Header.Set("Content-Type", fullsessionexport.MediaType)
	httpRequest.Header.Set("Content-Encoding", "gzip")
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	httpRequest.Header.Set("Coslash-Destination-Workspace-Id", request.Consent.DestinationWorkspaceID)
	httpRequest.Header.Set("Coslash-Destination-Audience-Version", request.Consent.AudienceVersion)
	response, err := c.fullSessionHTTPClient().Do(httpRequest)
	if err != nil {
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			if accepted, ok := c.lookupAcceptedFullSession(ctx, credential, request); ok {
				return accepted, nil
			}
			return fullSessionFailed(request, "timeout", true), fmt.Errorf("upload full session: %w", err)
		}
		return fullSessionFailed(request, "network_unavailable", true), fmt.Errorf("upload full session: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		problem := readProblem(response)
		code, retryable := mapFullSessionProblem(problem.Code)
		return fullSessionFailed(request, code, retryable), fmt.Errorf("upload full session: %w", problem)
	}
	var upload fullSessionUploadResponse
	if err := decodeBounded(response.Body, &upload); err != nil {
		return fullSessionFailed(request, "temporary_unavailable", true), fmt.Errorf("decode full-session upload response: %w", err)
	}
	if !validFullSessionUpload(upload, request.Selection, request.Consent) {
		return fullSessionFailed(request, "temporary_unavailable", true), errors.New("decode full-session upload response: contract mismatch")
	}
	return acceptedFullSession(request, upload), nil
}

func compressFullSession(payload []byte) ([]byte, error) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func (c *Client) fullSessionHTTPClient() *http.Client {
	client := c.httpClient()
	if client.Timeout > 0 && client.Timeout < fullSessionTimeout {
		client.Timeout = fullSessionTimeout
	}
	return client
}

func validFullSessionUpload(upload fullSessionUploadResponse, selection FullSessionSelection, consent FullSessionConsent) bool {
	wantPath := "/v2/sources/" + url.PathEscape(selection.SourceID) + "/agents/" + url.PathEscape(selection.Agent) +
		"/sessions/" + url.PathEscape(selection.SessionID) + "/revisions/" + url.PathEscape(selection.RevisionID)
	return upload.SourceID == selection.SourceID && upload.Agent == selection.Agent && upload.SessionID == selection.SessionID &&
		upload.RevisionID == selection.RevisionID && upload.RepositoryID != "" && upload.ByteCount == consent.RecordBytes &&
		upload.ContentSHA256 == consent.RecordSHA256 && !upload.SharedAt.IsZero() && upload.RevisionURL == wantPath
}

func acceptedFullSession(request FullSessionShareRequest, upload fullSessionUploadResponse) FullSessionShareResult {
	state := "accepted"
	if upload.Deduplicated {
		state = "already_accepted"
	}
	return FullSessionShareResult{
		ContractVersion: fullsessionexport.ShareVersion, State: state, Selection: request.Selection,
		IdempotencyKey: request.IdempotencyKey, RepositoryID: upload.RepositoryID, ByteCount: upload.ByteCount,
		ContentSHA256: upload.ContentSHA256, Deduplicated: upload.Deduplicated, SharedAt: &upload.SharedAt,
		Route: &FullSessionRoute{HubContractVersion: "full-session-read/v1", Path: upload.RevisionURL},
	}
}

func (c *Client) lookupAcceptedFullSession(ctx context.Context, credential string, request FullSessionShareRequest) (FullSessionShareResult, bool) {
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(lookupCtx, http.MethodGet, c.endpoint("/v2/uploads/status"), nil)
	if err != nil {
		return FullSessionShareResult{}, false
	}
	httpRequest.Header.Set("Authorization", "Device "+credential)
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	response, err := c.httpClient().Do(httpRequest)
	if err != nil {
		return FullSessionShareResult{}, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return FullSessionShareResult{}, false
	}
	var upload fullSessionUploadResponse
	if decodeBounded(response.Body, &upload) != nil || !validFullSessionUpload(upload, request.Selection, request.Consent) {
		return FullSessionShareResult{}, false
	}
	return acceptedFullSession(request, upload), true
}

func mapFullSessionProblem(code string) (string, bool) {
	switch code {
	case "full_session_too_large":
		return "full_session_too_large", false
	case "unsupported_full_session":
		return "incompatible_server", false
	case "full_session_invalid", "idempotency_conflict":
		return code, false
	case "destination_changed", "forbidden":
		return "review_binding_changed", true
	case "device_dormant":
		return "credential_dormant", true
	case "device_revoked":
		return "credential_revoked", false
	case "unauthorized":
		return "unauthorized", true
	case "rate_limited", "temporary_unavailable":
		return code, true
	default:
		return "temporary_unavailable", true
	}
}
