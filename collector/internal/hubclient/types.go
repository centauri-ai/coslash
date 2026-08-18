package hubclient

import "time"

const (
	ContractVersion = "hub-share/v1"
	PreviewVersion  = "snapshot-preview/v1"
)

type Destination struct {
	WorkspaceID                 string `json:"workspaceId"`
	WorkspaceName               string `json:"workspaceName"`
	CurrentMemberCount          int    `json:"currentMemberCount"`
	ResultingMemberCount        int    `json:"resultingMemberCount"`
	CurrentApprovedSessionCount int    `json:"currentApprovedSessionCount"`
	HistoryDisclosure           string `json:"historyDisclosure"`
	CredentialState             string `json:"credentialState"`
}

type DestinationResult struct {
	ContractVersion string       `json:"contractVersion"`
	State           string       `json:"state"`
	Destination     *Destination `json:"destination,omitempty"`
	Configured      bool         `json:"configured"`
	HubURL          string       `json:"hubUrl,omitempty"`
}

type ConsentBinding struct {
	PreviewContractVersion string `json:"previewContractVersion"`
	SourceRevision         int64  `json:"sourceRevision"`
	ContentHash            string `json:"contentHash"`
	PayloadBytes           int    `json:"payloadBytes"`
	DestinationWorkspaceID string `json:"destinationWorkspaceId"`
}

type ShareItemRequest struct {
	LocalSessionID string         `json:"localSessionId"`
	IdempotencyKey string         `json:"idempotencyKey"`
	Consent        ConsentBinding `json:"consent"`
}

type ShareRequest struct {
	ContractVersion string             `json:"contractVersion"`
	RequestID       string             `json:"requestId"`
	Items           []ShareItemRequest `json:"items"`
}

type RouteHandoff struct {
	HubContractVersion string `json:"hubContractVersion"`
	RepositoryID       string `json:"repositoryId"`
	CanonicalWeekStart string `json:"canonicalWeekStart"`
	Path               string `json:"path"`
}

type ItemError struct {
	Code              string `json:"code"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds *int   `json:"retryAfterSeconds,omitempty"`
}

type ShareItemResult struct {
	LocalSessionID string        `json:"localSessionId"`
	IdempotencyKey string        `json:"idempotencyKey"`
	State          string        `json:"state"`
	SessionID      string        `json:"sessionId,omitempty"`
	RevisionID     string        `json:"revisionId,omitempty"`
	Deduplicated   bool          `json:"deduplicated"`
	SharedAt       *time.Time    `json:"sharedAt,omitempty"`
	BriefState     string        `json:"briefState,omitempty"`
	Route          *RouteHandoff `json:"route,omitempty"`
	Error          *ItemError    `json:"error,omitempty"`
}

type ShareResult struct {
	ContractVersion string            `json:"contractVersion"`
	RequestID       string            `json:"requestId"`
	State           string            `json:"state"`
	Results         []ShareItemResult `json:"results"`
}

type PairingResult struct {
	State                   string    `json:"state"`
	PairingID               string    `json:"pairingId,omitempty"`
	UserCode                string    `json:"userCode,omitempty"`
	VerificationURI         string    `json:"verificationUri,omitempty"`
	VerificationURIComplete string    `json:"verificationUriComplete,omitempty"`
	ExpiresAt               time.Time `json:"expiresAt,omitempty"`
	IntervalSeconds         int       `json:"intervalSeconds,omitempty"`
}
