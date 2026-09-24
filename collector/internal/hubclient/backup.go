package hubclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	sessionbackupv1 "github.com/centauri-ai/coslash/collector/sessionbackup/v1"
)

const (
	BackupPreviewVersion  = "backup-preview/v1"
	BackupShareVersion    = "hub-share/v1"
	backupUploadVersion   = "backup-upload/v1"
	backupTimeout         = 30 * time.Minute
	backupWorkspaceHeader = "Coslash-Destination-Workspace-Id"
	backupAudienceHeader  = "Coslash-Destination-Audience-Version"
)

type BackupCapability struct {
	ServerID                   string `json:"serverId"`
	MaxBackupBytes             int64  `json:"maxBackupBytes"`
	MaxBackupChunkBytes        int64  `json:"maxBackupChunkBytes"`
	BackupWorkspaceBytes       int64  `json:"backupWorkspaceBytes"`
	BackupUploadExpiresSeconds int64  `json:"backupUploadExpiresSeconds"`
}

type BackupPreviewProblem struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Action    string `json:"action"`
	Retryable bool   `json:"retryable"`
}

type BackupPreview struct {
	AdapterVersion  string                          `json:"adapterVersion"`
	State           string                          `json:"state"`
	ApprovalAllowed bool                            `json:"approvalAllowed"`
	Selection       sessionbackupproducer.Selection `json:"selection"`
	BundleID        string                          `json:"bundleId,omitempty"`
	SourceRevision  string                          `json:"sourceRevision,omitempty"`
	Coverage        sessionbackupproducer.Coverage  `json:"coverage"`
	Capability      *BackupCapability               `json:"capability,omitempty"`
	AudienceVersion string                          `json:"audienceVersion,omitempty"`
	Problem         *BackupPreviewProblem           `json:"problem,omitempty"`
}

type BackupConsent struct {
	PreviewContractVersion string `json:"previewContractVersion"`
	BundleID               string `json:"bundleId"`
	SourceRevision         string `json:"sourceRevision"`
	SelectedRevision       int64  `json:"selectedRevision"`
	CompleteBackupSHA256   string `json:"completeBackupSha256"`
	TotalBytes             int64  `json:"totalBytes"`
	DestinationWorkspaceID string `json:"destinationWorkspaceId"`
	DestinationName        string `json:"destinationName"`
	AudienceMemberCount    int    `json:"audienceMemberCount"`
	AudienceVersion        string `json:"audienceVersion"`
	ServerID               string `json:"serverId"`
	MaxBackupBytes         int64  `json:"maxBackupBytes"`
	MaxBackupChunkBytes    int64  `json:"maxBackupChunkBytes"`
	BackupWorkspaceBytes   int64  `json:"backupWorkspaceBytes"`
}

type BackupShareItemRequest struct {
	LocalSessionID string                          `json:"localSessionId"`
	Selection      sessionbackupproducer.Selection `json:"selection"`
	IdempotencyKey string                          `json:"idempotencyKey"`
	Consent        BackupConsent                   `json:"consent"`
}

type BackupShareRequest struct {
	ContractVersion string                   `json:"contractVersion"`
	RequestID       string                   `json:"requestId"`
	Items           []BackupShareItemRequest `json:"items"`
}

type BackupRoute struct {
	HubContractVersion string `json:"hubContractVersion"`
	RepositoryID       string `json:"repositoryId"`
	Path               string `json:"path"`
}

type BackupShareItemResult struct {
	LocalSessionID string       `json:"localSessionId"`
	IdempotencyKey string       `json:"idempotencyKey"`
	State          string       `json:"state"`
	RevisionID     string       `json:"revisionId,omitempty"`
	Deduplicated   bool         `json:"deduplicated"`
	SharedAt       *time.Time   `json:"sharedAt,omitempty"`
	Route          *BackupRoute `json:"route,omitempty"`
	Error          *ItemError   `json:"error,omitempty"`
}

type BackupShareResult struct {
	ContractVersion string                  `json:"contractVersion"`
	RequestID       string                  `json:"requestId"`
	State           string                  `json:"state"`
	Results         []BackupShareItemResult `json:"results"`
}

type backupCapabilities struct {
	Product                    string   `json:"product"`
	ServerID                   string   `json:"serverId"`
	DisplayName                string   `json:"displayName"`
	ProtocolVersions           []string `json:"protocolVersions"`
	SnapshotVersions           []string `json:"snapshotVersions"`
	MaxSnapshotBytes           int64    `json:"maxSnapshotBytes"`
	FullSessionVersions        []string `json:"fullSessionVersions"`
	MaxFullSessionBytes        int64    `json:"maxFullSessionBytes"`
	MaxRequestBytes            int64    `json:"maxRequestBytes"`
	BackupVersions             []string `json:"backupVersions"`
	BackupUploadVersions       []string `json:"backupUploadVersions"`
	MaxBackupBytes             int64    `json:"maxBackupBytes"`
	MaxBackupChunkBytes        int64    `json:"maxBackupChunkBytes"`
	BackupWorkspaceBytes       int64    `json:"backupWorkspaceBytes"`
	BackupUploadExpiresSeconds int64    `json:"backupUploadExpiresSeconds"`
	PairingURL                 string   `json:"pairingUrl"`
	TeamURL                    string   `json:"teamUrl"`
}

func (c *Client) loadBackupCapabilities(ctx context.Context) (BackupCapability, error) {
	if c == nil || c.BaseURL == nil {
		return BackupCapability{}, capabilityFailure("incompatible_server", false, errors.New("Hub server is not configured"))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/.well-known/coslash-server"), nil)
	if err != nil {
		return BackupCapability{}, capabilityFailure("temporary_unavailable", true, err)
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		return BackupCapability{}, capabilityFailure("network_unavailable", true, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := fmt.Errorf("Hub capability request returned %d", response.StatusCode)
		if response.StatusCode >= http.StatusInternalServerError || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests {
			return BackupCapability{}, capabilityFailure("temporary_unavailable", true, failure)
		}
		return BackupCapability{}, capabilityFailure("incompatible_server", false, failure)
	}
	var value backupCapabilities
	if err := decodeBounded(response.Body, &value); err != nil {
		return BackupCapability{}, capabilityFailure("temporary_unavailable", true, err)
	}
	if value.Product != "coslash-server" || value.ServerID == "" ||
		!slices.Contains(value.ProtocolVersions, "v3") ||
		!slices.Contains(value.BackupVersions, sessionbackupv1.SchemaVersion) ||
		!slices.Contains(value.BackupUploadVersions, backupUploadVersion) ||
		value.MaxBackupBytes <= 0 || value.MaxBackupChunkBytes <= 0 || value.BackupWorkspaceBytes <= 0 ||
		value.BackupUploadExpiresSeconds <= 0 {
		return BackupCapability{}, capabilityFailure("incompatible_server", false, errors.New("Hub does not advertise compatible complete-backup support"))
	}
	return BackupCapability{
		ServerID: value.ServerID, MaxBackupBytes: value.MaxBackupBytes,
		MaxBackupChunkBytes: value.MaxBackupChunkBytes, BackupWorkspaceBytes: value.BackupWorkspaceBytes,
		BackupUploadExpiresSeconds: value.BackupUploadExpiresSeconds,
	}, nil
}

func backupPreviewFailure(selection sessionbackupproducer.Selection, state, code, message, action string, retryable bool) BackupPreview {
	return BackupPreview{
		AdapterVersion: BackupPreviewVersion, State: state, Selection: selection,
		Coverage: sessionbackupproducer.Coverage{Problems: []sessionbackupv1.CaptureProblem{}},
		Problem:  &BackupPreviewProblem{Code: code, Message: message, Action: action, Retryable: retryable},
	}
}

func (c *Client) PrepareBackup(ctx context.Context, selection sessionbackupproducer.Selection) (BackupPreview, error) {
	if c == nil || !c.configured() || c.Backup == nil || selection.SourceID == "" || selection.Agent == "" || selection.SessionID == "" ||
		(selection.SourceKind != sessionbackupv1.SourceLocal && selection.SourceKind != sessionbackupv1.SourceSSH) {
		return backupPreviewFailure(selection, "invalid", "invalid_share_request", "This session selection is invalid.", "Refresh sessions and select it again.", false), nil
	}
	if selection.Agent != "codex" {
		return backupPreviewFailure(selection, "blocked", "complete_backup_unsupported", "Complete backup currently supports local and SSH Codex sessions only.", "Choose a Codex session to share. This session remains local.", false), nil
	}
	destination, err := c.Destination(ctx)
	if err != nil {
		return backupPreviewFailure(selection, "unavailable", "temporary_unavailable", "The paired destination could not be verified.", "Retry the preparation.", true), err
	}
	if destination.State != "ready" || destination.Destination == nil || !validBackupAudienceVersion(destination.Destination.AudienceVersion) {
		return backupPreviewFailure(selection, "unavailable", "unauthorized", "A paired Hub destination is required.", "Pair this device and select a workspace.", true), nil
	}
	capability, err := c.loadBackupCapabilities(ctx)
	if err != nil {
		code, retryable := classifyCapabilityFailure(err)
		if code == "incompatible_server" {
			return backupPreviewFailure(selection, "incompatible_server", code, "This Hub cannot accept complete session backups.", "Update the Hub before sharing. coSlash will not send a metadata-only fallback.", false), nil
		}
		return backupPreviewFailure(selection, "unavailable", code, "The Hub capability check is unavailable.", "Retry without changing the selection.", retryable), err
	}
	prepared, err := c.Backup.Prepare(ctx, selection)
	if err != nil {
		preview := backupPreviewFailure(selection, "blocked", "artifact_unavailable", "A complete backup could not be prepared.", "Resolve the reported source problem and retry.", true)
		var preparationError *sessionbackupproducer.PreparationError
		if errors.As(err, &preparationError) {
			preview.Coverage = preparationError.Coverage
			if len(preparationError.Coverage.Problems) > 0 {
				problem := preparationError.Coverage.Problems[0]
				preview.Problem.Code = problem.Code
				preview.Problem.Retryable = problem.Retryable
			}
		}
		return preview, err
	}
	if prepared.Coverage.TotalBytes > capability.MaxBackupBytes {
		return BackupPreview{
			AdapterVersion: BackupPreviewVersion, State: "capacity_rejected", Selection: selection,
			BundleID: prepared.BundleID, SourceRevision: prepared.Manifest.Source.SourceRevision,
			Coverage: prepared.Coverage, Capability: &capability, AudienceVersion: destination.Destination.AudienceVersion,
			Problem: &BackupPreviewProblem{Code: "backup_capacity_exceeded", Message: "This complete backup exceeds the Hub per-backup capacity.", Action: "Ask the Hub operator to raise the configured capacity.", Retryable: false},
		}, nil
	}
	return BackupPreview{
		AdapterVersion: BackupPreviewVersion, State: "ready", ApprovalAllowed: true,
		Selection: selection, BundleID: prepared.BundleID, SourceRevision: prepared.Manifest.Source.SourceRevision,
		Coverage: prepared.Coverage, Capability: &capability, AudienceVersion: destination.Destination.AudienceVersion,
	}, nil
}

type backupChunkSpec struct {
	ArtifactOrdinal int    `json:"artifactOrdinal"`
	ChunkOrdinal    int    `json:"chunkOrdinal"`
	Offset          int64  `json:"offset"`
	ByteCount       int64  `json:"byteCount"`
	SHA256          string `json:"sha256"`
}

type backupChunkReceipt struct {
	ArtifactOrdinal int    `json:"artifactOrdinal"`
	ChunkOrdinal    int    `json:"chunkOrdinal"`
	ByteCount       int64  `json:"byteCount"`
	SHA256          string `json:"sha256"`
}

type backupUploadResult struct {
	RevisionID           string    `json:"revisionId"`
	CompleteBackupSHA256 string    `json:"completeBackupSha256"`
	RepositoryID         string    `json:"repositoryId"`
	SharedAt             time.Time `json:"sharedAt"`
	RevisionURL          string    `json:"revisionUrl"`
}

type backupUploadStatus struct {
	UploadID             string               `json:"uploadId"`
	State                string               `json:"state"`
	CompleteBackupSHA256 string               `json:"completeBackupSha256"`
	TotalBytes           int64                `json:"totalBytes"`
	ExpectedChunks       int                  `json:"expectedChunks"`
	ReceivedBytes        int64                `json:"receivedBytes"`
	ReceivedChunks       []backupChunkReceipt `json:"receivedChunks"`
	ExpiresAt            time.Time            `json:"expiresAt"`
	Result               *backupUploadResult  `json:"result,omitempty"`
}

func failedBackup(item BackupShareItemRequest, code string, retryable bool, retryAfter *int) BackupShareItemResult {
	code = mapBackupProblem(code)
	return BackupShareItemResult{
		LocalSessionID: item.LocalSessionID, IdempotencyKey: item.IdempotencyKey, State: "failed",
		Error: &ItemError{Code: code, Retryable: retryable, RetryAfterSeconds: retryAfter},
	}
}

func acceptedBackup(item BackupShareItemRequest, status backupUploadStatus, alreadyAccepted bool) BackupShareItemResult {
	result := status.Result
	state := "accepted"
	if alreadyAccepted {
		state = "already_accepted"
	}
	return BackupShareItemResult{
		LocalSessionID: item.LocalSessionID, IdempotencyKey: item.IdempotencyKey, State: state,
		RevisionID: result.RevisionID, SharedAt: &result.SharedAt,
		Deduplicated: alreadyAccepted,
		Route:        &BackupRoute{HubContractVersion: "session-backup-read/v1", RepositoryID: result.RepositoryID, Path: result.RevisionURL},
	}
}

func validCompletedBackupResult(result *backupUploadResult, consent BackupConsent) bool {
	if result == nil || result.RevisionID == "" || result.RepositoryID == "" || result.SharedAt.IsZero() ||
		result.CompleteBackupSHA256 != consent.CompleteBackupSHA256 {
		return false
	}
	return result.RevisionURL == "/v3/session-backups/"+url.PathEscape(result.RevisionID)
}

func validBackupShareItem(item BackupShareItemRequest) bool {
	consent := item.Consent
	return item.LocalSessionID != "" && item.Selection.SourceID != "" && item.Selection.Agent != "" && item.Selection.SessionID != "" &&
		len(item.IdempotencyKey) >= 16 && len(item.IdempotencyKey) <= 200 &&
		consent.PreviewContractVersion == BackupPreviewVersion && consent.BundleID == consent.CompleteBackupSHA256 &&
		len(consent.BundleID) == 64 && consent.SourceRevision != "" && consent.SelectedRevision > 0 && consent.TotalBytes >= 0 &&
		consent.DestinationWorkspaceID != "" && consent.DestinationName != "" && consent.AudienceMemberCount >= 0 &&
		validBackupAudienceVersion(consent.AudienceVersion) && consent.ServerID != "" && consent.MaxBackupBytes > 0 &&
		consent.MaxBackupChunkBytes > 0 && consent.BackupWorkspaceBytes > 0
}

func backupShareEligibility(item BackupShareItemRequest) string {
	selection := item.Selection
	if selection.SourceKind != sessionbackupv1.SourceLocal && selection.SourceKind != sessionbackupv1.SourceSSH {
		return "invalid_share_request"
	}
	if selection.Agent != "codex" {
		return "complete_backup_unsupported"
	}
	sourceID, agent, sessionID, ok := parseShareSessionID(item.LocalSessionID)
	if !ok || sourceID != selection.SourceID || agent != selection.Agent || sessionID != selection.SessionID {
		return "invalid_share_request"
	}
	return ""
}

func (c *Client) ShareBackups(ctx context.Context, request BackupShareRequest) (BackupShareResult, error) {
	result := BackupShareResult{ContractVersion: BackupShareVersion, RequestID: request.RequestID}
	if c == nil || !c.configured() || c.Backup == nil || request.ContractVersion != BackupShareVersion ||
		strings.TrimSpace(request.RequestID) == "" || len(request.Items) == 0 || len(request.Items) > 100 {
		return result, errors.New("invalid complete-backup share request")
	}
	credential, err := c.Credentials.Load(ctx)
	if errors.Is(err, ErrNotPaired) {
		for _, item := range request.Items {
			result.Results = append(result.Results, failedBackup(item, "unauthorized", true, nil))
		}
		result.State = "failed"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	shareContext, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	accepted := 0
	for _, item := range request.Items {
		itemResult, _ := c.shareBackupItem(shareContext, credential, item)
		if itemResult.State != "failed" {
			accepted++
			_ = c.Backup.Discard(item.Consent.BundleID)
		}
		result.Results = append(result.Results, itemResult)
	}
	switch {
	case accepted == len(request.Items):
		result.State = "succeeded"
	case accepted == 0:
		result.State = "failed"
	default:
		result.State = "partial"
	}
	return result, nil
}

func (c *Client) shareBackupItem(ctx context.Context, credential string, item BackupShareItemRequest) (BackupShareItemResult, error) {
	if !validBackupShareItem(item) {
		return failedBackup(item, "invalid_share_request", false, nil), nil
	}
	if code := backupShareEligibility(item); code != "" {
		return failedBackup(item, code, false, nil), nil
	}
	destination, err := c.Destination(ctx)
	if err != nil {
		return failedBackup(item, "temporary_unavailable", true, nil), errors.New("verify backup destination")
	}
	consent := item.Consent
	if destination.State != "ready" || destination.Destination == nil ||
		destination.Destination.WorkspaceID != consent.DestinationWorkspaceID ||
		destination.Destination.WorkspaceName != consent.DestinationName ||
		destination.Destination.CurrentMemberCount != consent.AudienceMemberCount ||
		destination.Destination.AudienceVersion != consent.AudienceVersion {
		return failedBackup(item, "destination_changed", true, nil), nil
	}
	capability, err := c.loadBackupCapabilities(ctx)
	if err != nil {
		code, retryable := classifyCapabilityFailure(err)
		return failedBackup(item, code, retryable, nil), errors.New("load backup capabilities")
	}
	if capability.ServerID != consent.ServerID || capability.MaxBackupBytes != consent.MaxBackupBytes ||
		capability.MaxBackupChunkBytes != consent.MaxBackupChunkBytes || capability.BackupWorkspaceBytes != consent.BackupWorkspaceBytes {
		return failedBackup(item, "stale_backup_review", true, nil), nil
	}
	prepared, err := c.Backup.Open(consent.BundleID)
	if err != nil {
		status, problem, statusErr := c.lookupBackupStatus(ctx, credential, item.IdempotencyKey)
		if statusErr != nil {
			if problem.Status == http.StatusNotFound || problem.Code == "not_found" {
				return failedBackup(item, "stale_backup_review", true, nil), nil
			}
			return failedBackup(item, problem.Code, backupRetryable(problem.Code), problem.RetryAfterSeconds), errors.New("lookup backup status")
		}
		if status.CompleteBackupSHA256 != consent.CompleteBackupSHA256 || status.TotalBytes != consent.TotalBytes {
			return failedBackup(item, "idempotency_conflict", false, nil), nil
		}
		if status.Result == nil {
			return failedBackup(item, "stale_backup_review", true, nil), nil
		}
		if status.State != "completed" || !validCompletedBackupResult(status.Result, consent) {
			return failedBackup(item, "idempotency_conflict", false, nil), nil
		}
		return acceptedBackup(item, status, true), nil
	}
	if prepared.Selection != item.Selection || prepared.Manifest.Source.SourceRevision != consent.SourceRevision ||
		prepared.Coverage.RevisionSHA256 != consent.CompleteBackupSHA256 || prepared.Coverage.TotalBytes != consent.TotalBytes {
		return failedBackup(item, "stale_backup_review", true, nil), nil
	}
	current, err := c.loadSourceSession(item.Selection.SourceID, item.Selection.Agent, item.Selection.SessionID, consent.SelectedRevision)
	if err != nil || current == nil || current.Agent != item.Selection.Agent || current.LastActivityTime != consent.SelectedRevision {
		return failedBackup(item, "stale_backup_review", true, nil), nil
	}
	plan, err := c.backupChunkPlan(prepared, capability.MaxBackupChunkBytes)
	if err != nil {
		return failedBackup(item, "temporary_unavailable", true, nil), errors.New("build backup chunk plan")
	}
	status, problem, err := c.createOrResumeBackup(ctx, credential, item, prepared, plan)
	if err != nil {
		return failedBackup(item, problem.Code, backupRetryable(problem.Code), problem.RetryAfterSeconds), err
	}
	if !validBackupStatusBinding(status, consent, len(plan)) {
		return failedBackup(item, "idempotency_conflict", false, nil), nil
	}
	if status.Result != nil {
		if status.State != "completed" || !validCompletedBackupResult(status.Result, consent) {
			return failedBackup(item, "idempotency_conflict", false, nil), nil
		}
		return acceptedBackup(item, status, true), nil
	}
	received := make(map[[2]int]backupChunkReceipt, len(status.ReceivedChunks))
	for _, chunk := range status.ReceivedChunks {
		received[[2]int{chunk.ArtifactOrdinal, chunk.ChunkOrdinal}] = chunk
	}
	for _, chunk := range plan {
		if receipt, ok := received[[2]int{chunk.ArtifactOrdinal, chunk.ChunkOrdinal}]; ok &&
			receipt.ByteCount == chunk.ByteCount && receipt.SHA256 == chunk.SHA256 {
			continue
		}
		body, readErr := c.readBackupChunk(prepared, chunk)
		if readErr != nil {
			return failedBackup(item, "temporary_unavailable", true, nil), errors.New("read frozen backup chunk")
		}
		status, problem, err = c.sendBackupChunk(ctx, credential, consent.DestinationWorkspaceID,
			consent.AudienceVersion, status.UploadID, chunk, body)
		if err != nil {
			return failedBackup(item, problem.Code, backupRetryable(problem.Code), problem.RetryAfterSeconds), err
		}
		if status.CompleteBackupSHA256 != consent.CompleteBackupSHA256 || status.TotalBytes != consent.TotalBytes {
			return failedBackup(item, "temporary_unavailable", true, nil), errors.New("backup chunk status mismatch")
		}
	}
	status, problem, err = c.finalizeBackup(ctx, credential, consent.DestinationWorkspaceID,
		consent.AudienceVersion, status.UploadID)
	if err != nil {
		return failedBackup(item, problem.Code, backupRetryable(problem.Code), problem.RetryAfterSeconds), err
	}
	if status.State != "completed" || !validCompletedBackupResult(status.Result, consent) {
		return failedBackup(item, "temporary_unavailable", true, nil), errors.New("complete backup response mismatch")
	}
	return acceptedBackup(item, status, false), nil
}

func validBackupStatusBinding(status backupUploadStatus, consent BackupConsent, expectedChunks int) bool {
	return status.CompleteBackupSHA256 == consent.CompleteBackupSHA256 && status.TotalBytes == consent.TotalBytes &&
		status.ExpectedChunks == expectedChunks
}

func (c *Client) backupChunkPlan(prepared *sessionbackupproducer.Prepared, chunkBytes int64) ([]backupChunkSpec, error) {
	if chunkBytes <= 0 || chunkBytes > int64(^uint(0)>>1) {
		return nil, errors.New("invalid chunk capacity")
	}
	plan := make([]backupChunkSpec, 0)
	for _, artifact := range prepared.Manifest.Artifacts {
		count := (artifact.ByteLength + chunkBytes - 1) / chunkBytes
		if count == 0 {
			count = 1
		}
		for ordinal := int64(0); ordinal < count; ordinal++ {
			offset := ordinal * chunkBytes
			length := min(chunkBytes, artifact.ByteLength-offset)
			body := make([]byte, int(length))
			if length > 0 {
				n, err := c.Backup.Read(prepared.BundleID, artifact.LogicalName, offset, body)
				if err != nil && !errors.Is(err, io.EOF) || int64(n) != length {
					return nil, errors.New("frozen artifact unavailable")
				}
			}
			sum := sha256.Sum256(body)
			plan = append(plan, backupChunkSpec{ArtifactOrdinal: artifact.Ordinal, ChunkOrdinal: int(ordinal), Offset: offset, ByteCount: length, SHA256: hex.EncodeToString(sum[:])})
		}
	}
	return plan, nil
}

func (c *Client) readBackupChunk(prepared *sessionbackupproducer.Prepared, chunk backupChunkSpec) ([]byte, error) {
	if chunk.ArtifactOrdinal < 0 || chunk.ArtifactOrdinal >= len(prepared.Manifest.Artifacts) {
		return nil, errors.New("artifact ordinal outside manifest")
	}
	body := make([]byte, int(chunk.ByteCount))
	if chunk.ByteCount > 0 {
		n, err := c.Backup.Read(prepared.BundleID, prepared.Manifest.Artifacts[chunk.ArtifactOrdinal].LogicalName, chunk.Offset, body)
		if err != nil && !errors.Is(err, io.EOF) || int64(n) != chunk.ByteCount {
			return nil, errors.New("frozen chunk unavailable")
		}
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != chunk.SHA256 {
		return nil, errors.New("frozen chunk changed")
	}
	return body, nil
}

func (c *Client) createOrResumeBackup(ctx context.Context, credential string, item BackupShareItemRequest, prepared *sessionbackupproducer.Prepared, plan []backupChunkSpec) (backupUploadStatus, Problem, error) {
	manifest, err := sessionbackupv1.Marshal(prepared.Manifest)
	if err != nil {
		return backupUploadStatus{}, Problem{Code: "backup_manifest_invalid"}, err
	}
	repositoryLocalOnly, err := c.repositoryLocalOnly(prepared)
	if err != nil {
		return backupUploadStatus{}, Problem{Code: "temporary_unavailable"}, err
	}
	body, err := json.Marshal(struct {
		Manifest                     json.RawMessage   `json:"manifest"`
		RepositoryLocalOnly          bool              `json:"repositoryLocalOnly"`
		ReviewedCompleteBackupSHA256 string            `json:"reviewedCompleteBackupSha256"`
		Chunks                       []backupChunkSpec `json:"chunks"`
	}{json.RawMessage(manifest), repositoryLocalOnly, item.Consent.CompleteBackupSHA256, plan})
	if err != nil {
		return backupUploadStatus{}, Problem{Code: "temporary_unavailable"}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/v3/backup-uploads"), bytes.NewReader(body))
	if err != nil {
		return backupUploadStatus{}, Problem{Code: "temporary_unavailable"}, err
	}
	request.Header.Set("Authorization", "Device "+credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", item.IdempotencyKey)
	request.Header.Set(backupWorkspaceHeader, item.Consent.DestinationWorkspaceID)
	request.Header.Set(backupAudienceHeader, item.Consent.AudienceVersion)
	status, problem, err := c.doBackupStatus(request, http.StatusCreated, http.StatusOK)
	if err == nil {
		return status, problem, nil
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, context.DeadlineExceeded) || problem.Code == "idempotency_conflict" {
		if recovered, recoveredProblem, recoveredErr := c.lookupBackupStatus(ctx, credential, item.IdempotencyKey); recoveredErr == nil {
			return recovered, recoveredProblem, nil
		}
	}
	return status, problem, err
}

func (c *Client) repositoryLocalOnly(prepared *sessionbackupproducer.Prepared) (bool, error) {
	for _, artifact := range prepared.Manifest.Artifacts {
		if artifact.MemberID != prepared.Manifest.Family.RootMemberID || artifact.Kind != sessionbackupv1.KindSessionEnrichment {
			continue
		}
		if artifact.ByteLength < 0 || artifact.ByteLength > 1<<20 {
			return false, errors.New("enrichment outside bounds")
		}
		body := make([]byte, int(artifact.ByteLength))
		n, err := c.Backup.Read(prepared.BundleID, artifact.LogicalName, 0, body)
		if err != nil && !errors.Is(err, io.EOF) || int64(n) != artifact.ByteLength {
			return false, errors.New("read root enrichment")
		}
		var enrichment sessionbackupv1.Enrichment
		if err := json.Unmarshal(body, &enrichment); err != nil {
			return false, errors.New("decode root enrichment")
		}
		return enrichment.RepositoryLocalOnly, nil
	}
	return false, errors.New("root enrichment missing")
}

func (c *Client) lookupBackupStatus(ctx context.Context, credential, key string) (backupUploadStatus, Problem, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/v3/backup-uploads/status"), nil)
	if err != nil {
		return backupUploadStatus{}, Problem{Code: "temporary_unavailable"}, err
	}
	request.Header.Set("Authorization", "Device "+credential)
	request.Header.Set("Idempotency-Key", key)
	return c.doBackupStatus(request, http.StatusOK)
}

func (c *Client) sendBackupChunk(ctx context.Context, credential, destination, audienceVersion, uploadID string, chunk backupChunkSpec, body []byte) (backupUploadStatus, Problem, error) {
	path := fmt.Sprintf("/v3/backup-uploads/%s/artifacts/%d/chunks/%d", uploadID, chunk.ArtifactOrdinal, chunk.ChunkOrdinal)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpoint(path), bytes.NewReader(body))
	if err != nil {
		return backupUploadStatus{}, Problem{Code: "temporary_unavailable"}, err
	}
	request.Header.Set("Authorization", "Device "+credential)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(backupWorkspaceHeader, destination)
	request.Header.Set(backupAudienceHeader, audienceVersion)
	return c.doBackupStatus(request, http.StatusOK)
}

func (c *Client) finalizeBackup(ctx context.Context, credential, destination, audienceVersion, uploadID string) (backupUploadStatus, Problem, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/v3/backup-uploads/"+uploadID+"/finalize"), nil)
	if err != nil {
		return backupUploadStatus{}, Problem{Code: "temporary_unavailable"}, err
	}
	request.Header.Set("Authorization", "Device "+credential)
	request.Header.Set(backupWorkspaceHeader, destination)
	request.Header.Set(backupAudienceHeader, audienceVersion)
	return c.doBackupStatus(request, http.StatusOK)
}

func validBackupAudienceVersion(value string) bool {
	const prefix = "audience-v1:"
	if len(value) != len(prefix)+64 || !strings.HasPrefix(value, prefix) || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}

func (c *Client) doBackupStatus(request *http.Request, accepted ...int) (backupUploadStatus, Problem, error) {
	response, err := c.httpClient().Do(request)
	if err != nil {
		return backupUploadStatus{}, Problem{Code: "network_unavailable"}, err
	}
	defer response.Body.Close()
	if !slices.Contains(accepted, response.StatusCode) {
		problem := readProblem(response)
		return backupUploadStatus{}, problem, fmt.Errorf("backup request failed (status %d, code %.200q)", response.StatusCode, problem.Code)
	}
	var status backupUploadStatus
	if err := decodeBounded(response.Body, &status); err != nil || status.UploadID == "" || status.CompleteBackupSHA256 == "" {
		return backupUploadStatus{}, Problem{Code: "temporary_unavailable"}, errors.New("decode backup upload status")
	}
	return status, Problem{}, nil
}

func backupRetryable(code string) bool {
	switch mapBackupProblem(code) {
	case "backup_manifest_invalid", "backup_chunk_invalid", "backup_capacity_exceeded", "source_deleted", "incompatible_server":
		return false
	default:
		return true
	}
}

func mapBackupProblem(code string) string {
	switch code {
	case "device_dormant":
		return "credential_dormant"
	case "device_revoked":
		return "credential_revoked"
	case "request_too_large":
		return "backup_manifest_invalid"
	case "invalid_share_request", "complete_backup_unsupported", "incompatible_server", "backup_manifest_invalid",
		"stale_backup_review", "backup_chunk_invalid", "backup_incomplete", "backup_capacity_exceeded",
		"backup_upload_expired", "backup_upload_aborted", "not_found", "forbidden", "source_deleted",
		"unauthorized", "credential_dormant", "credential_revoked", "destination_changed", "idempotency_conflict",
		"rate_limited", "network_unavailable", "timeout", "temporary_unavailable", "share_failed":
		return code
	default:
		return "share_failed"
	}
}
